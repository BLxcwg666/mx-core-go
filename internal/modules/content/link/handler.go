package link

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
	pkgmail "github.com/mx-space/core/internal/pkg/mail"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
)

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
		response.NotFoundMsg(c, "友链不存在")
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
		response.NotFoundMsg(c, "友链不存在")
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
