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
	coreconfig "github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/gateway/gateway"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
	pkgmail "github.com/mx-space/core/internal/pkg/mail"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
)

type Handler struct {
	svc    *Service
	cfgSvc *appconfigs.Service
	hub    *gateway.Hub
}

func NewHandler(svc *Service, cfgSvc *appconfigs.Service, hub *gateway.Hub) *Handler {
	return &Handler{svc: svc, cfgSvc: cfgSvc, hub: hub}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	for _, prefix := range []string{"/links", "/friends"} {
		g := rg.Group(prefix)

		g.GET("", h.list)
		g.GET("/all", h.listAll)
		g.GET("/audit", h.canApply)
		g.GET("/state", h.stateCount)
		g.GET("/:id", h.get)

		g.POST("", h.create)
		g.POST("/audit", h.create)

		a := g.Group("", authMW)
		a.GET("/health", h.health)
		a.PATCH("/audit/:id", h.audit)
		a.POST("/audit/reason/:id", h.auditReason)
		a.POST("/avatar/migrate", h.migrateAvatars)
		a.PUT("/:id", h.update)
		a.PATCH("/:id", h.patch)
		a.DELETE("/:id", h.delete)
	}
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
					response.UnprocessableEntity(c, "主人当前禁用了子路径友链申请")
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
	if !isAdmin && h.hub != nil {
		h.hub.BroadcastAdmin("LINK_APPLY", toResponse(l, true))
	}
	if !isAdmin && h.cfgSvc != nil {
		go h.sendApplyNotification(l, dto.Author)
	}
	response.Created(c, toResponse(l, isAdmin))
}

func (h *Handler) get(c *gin.Context) {
	l, err := h.svc.GetByID(c.Param("id"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if l == nil {
		response.NotFoundMsg(c, "友链不存在")
		return
	}
	response.OK(c, toResponse(l, middleware.IsAuthenticated(c)))
}

// GET /links/health — health check
func (h *Handler) health(c *gin.Context) {
	result := h.svc.HealthCheck()
	response.OK(c, result)
}

// PATCH /links/audit/:id — approve link
func (h *Handler) audit(c *gin.Context) {
	l, err := h.svc.Approve(c.Param("id"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if l == nil {
		response.NotFoundMsg(c, "友链不存在")
		return
	}
	if l.Email != "" && h.cfgSvc != nil {
		go h.sendPassNotification(l)
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

func (h *Handler) patch(c *gin.Context) {
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
	response.NoContent(c)
}

func (h *Handler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Param("id")); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) sendAuditNotification(l *models.LinkModel, state models.LinkState, reason string) {
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
	_ = h.sendLinkNotification(l.Email, "嘿!~, 主人已处理你的友链申请!~", linkAuditTpl, linkAuditData{
		Name:       l.Name,
		URL:        l.URL,
		StateLabel: stateLabel,
		Reason:     reason,
	}, buildLinkAuditText(stateLabel, reason))
}

func (h *Handler) sendPassNotification(l *models.LinkModel) {
	_ = h.sendLinkNotification(l.Email, "嘿!~, 主人已通过你的友链申请!~", linkPassTpl, linkPassData{
		Name:        l.Name,
		URL:         l.URL,
		Description: l.Description,
	}, buildLinkPassText(l))
}

func (h *Handler) sendApplyNotification(l *models.LinkModel, authorName string) {
	if h.cfgSvc == nil {
		return
	}
	cfg, err := h.cfgSvc.Get()
	if err != nil || cfg == nil || !cfg.MailOptions.Enable {
		return
	}
	var owner models.UserModel
	if err := h.svc.db.Select("mail").First(&owner).Error; err != nil {
		return
	}
	if strings.TrimSpace(owner.Mail) == "" {
		return
	}
	authorName = strings.TrimSpace(authorName)
	if authorName == "" {
		authorName = l.Name
	}
	siteTitle := strings.TrimSpace(cfg.SEO.Title)
	if siteTitle == "" {
		siteTitle = "Mx Space"
	}
	_ = h.sendLinkNotificationWithConfig(cfg, owner.Mail, fmt.Sprintf("[%s] 新的朋友 %s", siteTitle, authorName), linkApplyTpl, linkApplyData{
		AuthorName:  authorName,
		Name:        l.Name,
		URL:         l.URL,
		Description: l.Description,
	}, buildLinkApplyText(authorName, l))
}

func (h *Handler) sendLinkNotification(to, subject, tplText string, data any, plainText string) error {
	if h.cfgSvc == nil || strings.TrimSpace(to) == "" {
		return nil
	}
	cfg, err := h.cfgSvc.Get()
	if err != nil || cfg == nil || !cfg.MailOptions.Enable {
		return err
	}
	return h.sendLinkNotificationWithConfig(cfg, to, subject, tplText, data, plainText)
}

func (h *Handler) sendLinkNotificationWithConfig(cfg *coreconfig.FullConfig, to, subject, tplText string, data any, plainText string) error {
	if cfg == nil || strings.TrimSpace(to) == "" {
		return nil
	}
	tpl, err := template.New("").Parse(tplText)
	if err != nil {
		return err
	}
	var buf strings.Builder
	if err := tpl.Execute(&buf, data); err != nil {
		return err
	}
	sender := pkgmail.New(pkgmail.BuildMailConfig(cfg), pkgmail.WithLogger(h.svc.logger))
	return sender.Send(pkgmail.Message{
		To:      []string{to},
		Subject: subject,
		HTML:    buf.String(),
		Text:    plainText,
	})
}

func buildLinkAuditText(stateLabel, reason string) string {
	text := "申请结果：" + stateLabel
	if trimmedReason := strings.TrimSpace(reason); trimmedReason != "" {
		text += "\n原因：" + trimmedReason
	}
	return text
}

func buildLinkPassText(l *models.LinkModel) string {
	if l == nil {
		return ""
	}
	return fmt.Sprintf("你的友链申请：%s, %s 已通过", l.Name, l.URL)
}

func buildLinkApplyText(authorName string, l *models.LinkModel) string {
	if l == nil {
		return ""
	}
	return fmt.Sprintf("来自 %s 的友链请求：\n站点标题：%s\n站点网站：%s\n站点描述：%s", authorName, l.Name, l.URL, l.Description)
}
