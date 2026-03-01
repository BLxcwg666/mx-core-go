package link

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	appconfigs "github.com/mx-space/core/internal/modules/configs"
	pkgmail "github.com/mx-space/core/internal/pkg/mail"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type ApplyLinkDTO struct {
	Name        string            `json:"name"        binding:"required"`
	URL         string            `json:"url"         binding:"required,url"`
	Avatar      string            `json:"avatar"`
	Description string            `json:"description"`
	Email       string            `json:"email"`
	Type        *models.LinkType  `json:"type"`
	State       *models.LinkState `json:"state"`
}

type UpdateLinkDTO struct {
	Name        *string           `json:"name"`
	URL         *string           `json:"url"`
	Avatar      *string           `json:"avatar"`
	Description *string           `json:"description"`
	State       *models.LinkState `json:"state"`
	Type        *models.LinkType  `json:"type"`
	Email       *string           `json:"email"`
}

type AuditReasonDTO struct {
	State  models.LinkState `json:"state"  binding:"required"`
	Reason string           `json:"reason"`
}

type linkResponse struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	URL         string           `json:"url"`
	Avatar      string           `json:"avatar"`
	Description string           `json:"description"`
	Type        models.LinkType  `json:"type"`
	State       models.LinkState `json:"state"`
	Email       string           `json:"email,omitempty"`
	Created     time.Time        `json:"created"`
	Modified    time.Time        `json:"modified"`
}

func toResponse(l *models.LinkModel, showEmail bool) linkResponse {
	r := linkResponse{
		ID: l.ID, Name: l.Name, URL: l.URL, Avatar: l.Avatar,
		Description: l.Description, Type: l.Type, State: l.State,
		Created: l.CreatedAt, Modified: l.UpdatedAt,
	}
	if showEmail {
		r.Email = l.Email
	}
	return r
}

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

var (
	errDuplicateLink      = errors.New("duplicate link")
	errLinkDisabled       = errors.New("link disabled")
	errSubpathLinkDisable = errors.New("subpath link disabled")
)

func (s *Service) List(q pagination.Query, state *models.LinkState) ([]models.LinkModel, response.Pagination, error) {
	tx := s.db.Model(&models.LinkModel{}).Order("created_at DESC")
	if state != nil {
		tx = tx.Where("state = ?", *state)
	}
	var items []models.LinkModel
	pag, err := pagination.Paginate(tx, q, &items)
	return items, pag, err
}

// ListAllVisible returns all publicly visible links.
func (s *Service) ListAllVisible() ([]models.LinkModel, error) {
	var items []models.LinkModel
	err := s.db.
		Where("state NOT IN ?", []models.LinkState{models.LinkAudit, models.LinkReject}).
		Order("created_at DESC").
		Find(&items).Error
	return items, err
}

func (s *Service) GetByID(id string) (*models.LinkModel, error) {
	var l models.LinkModel
	if err := s.db.First(&l, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

// Apply creates a link (Audit state for public, optionally Pass for admin).
func (s *Service) Apply(dto *ApplyLinkDTO, isAdmin bool) (*models.LinkModel, error) {
	var existed models.LinkModel
	err := s.db.Where("url = ? OR name = ?", dto.URL, dto.Name).First(&existed).Error
	if err == nil {
		if isAdmin {
			return nil, errDuplicateLink
		}
		switch existed.State {
		case models.LinkPass, models.LinkAudit:
			return nil, errDuplicateLink
		case models.LinkBanned:
			return nil, errLinkDisabled
		case models.LinkReject, models.LinkOutdate:
			if updateErr := s.db.Model(&existed).Update("state", models.LinkAudit).Error; updateErr != nil {
				return nil, updateErr
			}
			existed.State = models.LinkAudit
			return &existed, nil
		default:
			return nil, errDuplicateLink
		}
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	linkType := models.LinkTypeFriend
	if dto.Type != nil {
		linkType = *dto.Type
	}
	state := models.LinkAudit
	if isAdmin && dto.State != nil {
		state = *dto.State
	} else if isAdmin {
		state = models.LinkPass
	}

	l := models.LinkModel{
		Name: dto.Name, URL: dto.URL, Avatar: dto.Avatar,
		Description: dto.Description, Email: dto.Email,
		Type: linkType, State: state,
	}
	return &l, s.db.Create(&l).Error
}

func (s *Service) Update(id string, dto *UpdateLinkDTO) (*models.LinkModel, error) {
	l, err := s.GetByID(id)
	if err != nil || l == nil {
		return l, err
	}
	updates := map[string]interface{}{}
	if dto.Name != nil {
		updates["name"] = *dto.Name
	}
	if dto.URL != nil {
		updates["url"] = *dto.URL
	}
	if dto.Avatar != nil {
		updates["avatar"] = *dto.Avatar
	}
	if dto.Description != nil {
		updates["description"] = *dto.Description
	}
	if dto.State != nil {
		updates["state"] = *dto.State
	}
	if dto.Type != nil {
		updates["type"] = *dto.Type
	}
	if dto.Email != nil {
		updates["email"] = *dto.Email
	}
	return l, s.db.Model(l).Updates(updates).Error
}

func (s *Service) Delete(id string) error {
	return s.db.Delete(&models.LinkModel{}, "id = ?", id).Error
}

// Approve sets link state to Pass.
func (s *Service) Approve(id string) error {
	return s.db.Model(&models.LinkModel{}).Where("id = ?", id).Update("state", models.LinkPass).Error
}

// StateCount returns counts per state.
func (s *Service) StateCount() map[string]int64 {
	type row struct {
		State models.LinkState
		Count int64
	}
	var rows []row
	s.db.Model(&models.LinkModel{}).Select("state, COUNT(*) as count").Group("state").Scan(&rows)

	counts := map[string]int64{
		"pass": 0, "audit": 0, "outdate": 0, "banned": 0, "reject": 0,
		"friends": 0, "collection": 0,
	}
	stateNames := map[models.LinkState]string{
		models.LinkPass:    "pass",
		models.LinkAudit:   "audit",
		models.LinkOutdate: "outdate",
		models.LinkBanned:  "banned",
		models.LinkReject:  "reject",
	}
	for _, r := range rows {
		if name, ok := stateNames[r.State]; ok {
			counts[name] = r.Count
		}
	}

	var typeCounts []struct {
		Type  models.LinkType
		Count int64
	}
	s.db.Model(&models.LinkModel{}).Where("state = ?", models.LinkPass).
		Select("type, COUNT(*) as count").Group("type").Scan(&typeCounts)
	for _, tc := range typeCounts {
		if tc.Type == models.LinkTypeFriend {
			counts["friends"] = tc.Count
		} else if tc.Type == models.LinkTypeCollection {
			counts["collection"] = tc.Count
		}
	}
	return counts
}

type HealthResult struct {
	ID      string `json:"id"`
	Status  int    `json:"status"`
	Message string `json:"message,omitempty"`
}

func (s *Service) HealthCheck() map[string]HealthResult {
	var links []models.LinkModel
	s.db.Where("state = ?", models.LinkPass).Find(&links)

	result := make(map[string]HealthResult, len(links))
	client := &http.Client{Timeout: 10 * time.Second}

	for _, l := range links {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.URL, nil)
		cancel()
		if err != nil {
			result[l.ID] = HealthResult{ID: l.ID, Status: 0, Message: err.Error()}
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Mix-Space Friend Link Checker; +https://github.com/BLxcwg666/mx-core-go)")
		resp, err := client.Do(req)
		if err != nil {
			result[l.ID] = HealthResult{ID: l.ID, Status: 0, Message: err.Error()}
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			result[l.ID] = HealthResult{ID: l.ID, Status: resp.StatusCode,
				Message: fmt.Sprintf("HTTP %d", resp.StatusCode)}
		} else {
			result[l.ID] = HealthResult{ID: l.ID, Status: resp.StatusCode}
		}
	}
	return result
}

type Handler struct {
	svc    *Service
	cfgSvc *appconfigs.Service
}

func NewHandler(svc *Service, cfgSvc *appconfigs.Service) *Handler {
	return &Handler{svc: svc, cfgSvc: cfgSvc}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/links")

	g.GET("", h.list)
	g.GET("/all", h.listAll)
	g.GET("/audit", h.canApply)
	g.GET("/state", h.stateCount)

	g.POST("", h.create)
	g.POST("/audit", h.create)

	a := g.Group("", authMW)
	a.GET("/health", h.health)
	a.PATCH("/audit/:id", h.audit)
	a.POST("/audit/reason/:id", h.auditReason)
	a.POST("/avatar/migrate", h.migrateAvatars)
	a.PUT("/:id", h.update)
	a.DELETE("/:id", h.delete)
}

// GET /links/audit
func (h *Handler) canApply(c *gin.Context) {
	canApply := true
	if h.cfgSvc != nil {
		cfg, err := h.cfgSvc.Get()
		if err != nil {
			response.InternalError(c, err)
			return
		}
		if cfg != nil {
			canApply = cfg.FriendLinkOptions.AllowApply
		}
	}
	response.OK(c, gin.H{"can": canApply})
}

// GET /links?state=N
func (h *Handler) list(c *gin.Context) {
	q := pagination.FromContext(c)
	isAdmin := middleware.IsAuthenticated(c)

	var stateFilter *models.LinkState
	stateStr := c.Query("state")
	if stateStr != "" {
		var stateVal models.LinkState
		fmt.Sscanf(stateStr, "%d", &stateVal)
		if !isAdmin && stateVal != models.LinkPass {
			stateVal = models.LinkPass
		}
		stateFilter = &stateVal
	} else if !isAdmin {
		passState := models.LinkPass
		stateFilter = &passState
	}

	items, pag, err := h.svc.List(q, stateFilter)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	out := make([]linkResponse, len(items))
	for i, l := range items {
		out[i] = toResponse(&l, isAdmin)
	}
	response.Paged(c, out, pag)
}

// GET /links/all
func (h *Handler) listAll(c *gin.Context) {
	items, err := h.svc.ListAllVisible()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	isAdmin := middleware.IsAuthenticated(c)
	out := make([]linkResponse, len(items))
	for i, l := range items {
		out[i] = toResponse(&l, isAdmin)
	}
	response.OK(c, out)
}

// GET /links/state — count by state
func (h *Handler) stateCount(c *gin.Context) {
	response.OK(c, h.svc.StateCount())
}

// POST /links — apply (public) or admin create
func (h *Handler) create(c *gin.Context) {
	var dto ApplyLinkDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	isAdmin := middleware.IsAuthenticated(c)
	if !isAdmin && h.cfgSvc != nil {
		cfg, err := h.cfgSvc.Get()
		if err != nil {
			response.InternalError(c, err)
			return
		}
		if cfg != nil {
			if !cfg.FriendLinkOptions.AllowApply {
				response.ForbiddenMsg(c, "主人目前不允许申请友链了！")
				return
			}
			normalizedURL, normalizeErr := normalizeApplyLinkURL(dto.URL, cfg.FriendLinkOptions.AllowSubPath)
			if normalizeErr != nil {
				if errors.Is(normalizeErr, errSubpathLinkDisable) {
					response.UnprocessableEntity(c, "管理员当前禁用了子路径友链申请")
					return
				}
				response.BadRequest(c, normalizeErr.Error())
				return
			}
			dto.URL = normalizedURL
		}
	}
	l, err := h.svc.Apply(&dto, isAdmin)
	if err != nil {
		if errors.Is(err, errDuplicateLink) {
			if isAdmin {
				response.Conflict(c, "url already exists")
				return
			}
			response.BadRequest(c, "请不要重复申请友链哦")
			return
		}
		if errors.Is(err, errLinkDisabled) {
			response.BadRequest(c, "您的友链已被禁用，请联系管理员")
			return
		}
		if errors.Is(err, errSubpathLinkDisable) {
			response.UnprocessableEntity(c, "管理员当前禁用了子路径友链申请")
			return
		}
		response.InternalError(c, err)
		return
	}
	response.Created(c, toResponse(l, isAdmin))
}

// GET /links/health — health check
func (h *Handler) health(c *gin.Context) {
	result := h.svc.HealthCheck()
	response.OK(c, result)
}

// PATCH /links/audit/:id — approve link
func (h *Handler) audit(c *gin.Context) {
	if err := h.svc.Approve(c.Param("id")); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

// POST /links/audit/reason/:id — send audit result with reason
func (h *Handler) auditReason(c *gin.Context) {
	var dto AuditReasonDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	id := c.Param("id")
	l, err := h.svc.GetByID(id)
	if err != nil || l == nil {
		response.NotFound(c)
		return
	}

	updates := map[string]interface{}{"state": dto.State}
	if err := h.svc.db.Model(l).Updates(updates).Error; err != nil {
		response.InternalError(c, err)
		return
	}

	if l.Email != "" && h.cfgSvc != nil {
		go h.sendAuditNotification(l, dto.State, dto.Reason)
	}
	response.NoContent(c)
}

// POST /links/avatar/migrate — fetch favicons for links that have no avatar
func (h *Handler) migrateAvatars(c *gin.Context) {
	var links []models.LinkModel
	h.svc.db.Where("state = ? AND (avatar = '' OR avatar IS NULL)", models.LinkPass).Find(&links)

	client := &http.Client{Timeout: 10 * time.Second}
	updated := 0
	for _, l := range links {
		domain := extractDomain(l.URL)
		if domain == "" {
			continue
		}

		avatarURL := "https://icons.duckduckgo.com/ip3/" + domain + ".ico"
		resp, err := client.Get(avatarURL)
		if err != nil || resp.StatusCode >= 400 {
			if resp != nil {
				resp.Body.Close()
			}
			continue
		}
		resp.Body.Close()
		h.svc.db.Model(&l).Update("avatar", avatarURL)
		updated++
	}
	response.OK(c, gin.H{"message": "avatar migration completed", "updated": updated})
}

func (h *Handler) update(c *gin.Context) {
	var dto UpdateLinkDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	l, err := h.svc.Update(c.Param("id"), &dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if l == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, toResponse(l, true))
}

func (h *Handler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Param("id")); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

var linkAuditTpl = `<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;background:#f5f5f5;padding:20px">
<div style="max-width:600px;margin:0 auto;background:#fff;border-radius:8px;padding:24px">
  <h2 style="color:#333">友链申请审核结果</h2>
  <p>您的友链申请（<a href="{{.URL}}">{{.Name}}</a>）审核结果如下：</p>
  <p>状态：<strong>{{.StateLabel}}</strong></p>
  {{if .Reason}}<p>原因：{{.Reason}}</p>{{end}}
</div>
</body>
</html>`

type linkAuditData struct {
	Name       string
	URL        string
	StateLabel string
	Reason     string
}

func (h *Handler) sendAuditNotification(l *models.LinkModel, state models.LinkState, reason string) {
	cfg, err := h.cfgSvc.Get()
	if err != nil || cfg == nil || !cfg.MailOptions.Enable {
		return
	}
	stateLabel := map[models.LinkState]string{
		models.LinkPass:    "通过",
		models.LinkReject:  "拒绝",
		models.LinkBanned:  "封禁",
		models.LinkOutdate: "过期",
		models.LinkAudit:   "待审核",
	}[state]
	if stateLabel == "" {
		stateLabel = strconv.Itoa(int(state))
	}
	tpl, err := template.New("").Parse(linkAuditTpl)
	if err != nil {
		return
	}
	var buf strings.Builder
	if err := tpl.Execute(&buf, linkAuditData{
		Name:       l.Name,
		URL:        l.URL,
		StateLabel: stateLabel,
		Reason:     reason,
	}); err != nil {
		return
	}
	mailCfg := pkgmail.Config{
		Enable: cfg.MailOptions.Enable,
		From:   cfg.MailOptions.From,
	}
	if cfg.MailOptions.SMTP != nil {
		mailCfg.Host = cfg.MailOptions.SMTP.Options.Host
		mailCfg.Port = cfg.MailOptions.SMTP.Options.Port
		mailCfg.User = cfg.MailOptions.SMTP.User
		mailCfg.Pass = cfg.MailOptions.SMTP.Pass
	}
	if cfg.MailOptions.Resend != nil && cfg.MailOptions.Resend.APIKey != "" {
		mailCfg.UseResend = true
		mailCfg.ResendKey = cfg.MailOptions.Resend.APIKey
	}
	sender := pkgmail.New(mailCfg)
	_ = sender.Send(pkgmail.Message{
		To:      []string{l.Email},
		Subject: "嘿!~, 主人已处理你的友链申请!~",
		HTML:    buf.String(),
	})
}

func extractDomain(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	host = strings.TrimPrefix(host, "www.")
	return host
}

func normalizeApplyLinkURL(raw string, allowSubPath bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("invalid url")
	}
	origin := parsed.Scheme + "://" + parsed.Host
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if path != "/" && !allowSubPath {
		return "", errSubpathLinkDisable
	}
	if allowSubPath {
		return origin + path, nil
	}
	return origin, nil
}
