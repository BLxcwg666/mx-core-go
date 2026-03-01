package user

import (
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
	"github.com/mx-space/core/internal/pkg/response"
	sessionpkg "github.com/mx-space/core/internal/pkg/session"
	"gorm.io/gorm"
)

type Handler struct {
	svc    *Service
	cfgSvc *appconfigs.Service
}

func NewHandler(svc *Service, cfgSvc *appconfigs.Service) *Handler {
	return &Handler{svc: svc, cfgSvc: cfgSvc}
}

// RegisterRoutes registers routes under BOTH /master and /user for admin panel compatibility.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	for _, prefix := range []string{"/master", "/user", "/owner"} {
		g := rg.Group(prefix)

		g.GET("", middleware.OptionalAuth(h.svc.db), h.getMasterInfo)
		g.GET("/check_logged", middleware.OptionalAuth(h.svc.db), h.checkLogged)
		g.GET("/allow-login", h.allowLogin)
		g.POST("/login", h.login)
		g.POST("/register", h.register)

		a := g.Group("", authMW)
		a.PATCH("", h.updateProfile)
		a.PUT("/login", h.loginWithToken)
		a.POST("/logout", h.logout)
		a.PATCH("/password", h.changePassword)
		a.GET("/session", h.listSessions)
		a.DELETE("/session/all", h.deleteAllSessions)
		a.DELETE("/session/:tokenId", h.deleteSession)
	}
}

func (h *Handler) checkLogged(c *gin.Context) {
	isAuthenticated := middleware.IsAuthenticated(c)
	if !isAuthenticated {
		token := strings.TrimSpace(c.Query("token"))
		if strings.HasPrefix(strings.ToLower(token), "bearer ") {
			token = strings.TrimSpace(token[7:])
		}
		if token != "" {
			_, err := middleware.ValidateToken(h.svc.db, token)
			isAuthenticated = err == nil
		}
	}
	response.OK(c, gin.H{
		"ok":      boolToInt(isAuthenticated),
		"isGuest": !isAuthenticated,
	})
}

func (h *Handler) allowLogin(c *gin.Context) {
	var passkeyCount int64
	_ = h.svc.db.Model(&models.AuthnModel{}).Count(&passkeyCount).Error

	passwordEnabled := true
	enabledProviders := map[string]bool{}
	if h.cfgSvc != nil {
		if cfg, err := h.cfgSvc.Get(); err == nil && cfg != nil {
			passwordEnabled = !cfg.AuthSecurity.DisablePasswordLogin
			for _, provider := range cfg.OAuth.Providers {
				providerType := strings.ToLower(strings.TrimSpace(provider.Type))
				if providerType == "" || !provider.Enabled {
					continue
				}
				if oauthProviderClientID(cfg.OAuth.Public, providerType) == "" {
					continue
				}
				enabledProviders[providerType] = true
			}
		}
	}

	res := gin.H{
		"password": passwordEnabled,
		"passkey":  passkeyCount > 0,
	}
	for providerType, enabled := range enabledProviders {
		res[providerType] = enabled
	}
	response.OK(c, res)
}

func (h *Handler) login(c *gin.Context) {
	var dto LoginDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if h.cfgSvc != nil {
		cfg, err := h.cfgSvc.Get()
		if err != nil {
			response.InternalError(c, err)
			return
		}
		if cfg != nil && cfg.AuthSecurity.DisablePasswordLogin {
			response.BadRequest(c, "密码登录已禁用")
			return
		}
	}
	token, u, err := h.svc.Login(dto.Username, dto.Password, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		if errors.Is(err, errUserNotFound) {
			response.ForbiddenMsg(c, "用户名不正确")
			return
		}
		if errors.Is(err, errWrongPassword) {
			response.ForbiddenMsg(c, "密码不正确")
			return
		}
		response.InternalError(c, err)
		return
	}
	response.OK(c, loginResponse{Token: token, User: toResponse(u)})
}

func (h *Handler) register(c *gin.Context) {
	var dto RegisterDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	u, err := h.svc.Register(&dto)
	if err != nil {
		if errors.Is(err, errOwnerAlreadyExists) {
			response.BadRequest(c, "我已经有一个主人了哦")
			return
		}
		response.InternalError(c, err)
		return
	}
	response.Created(c, toResponse(u))
}

func (h *Handler) getMasterInfo(c *gin.Context) {
	u, err := h.svc.GetOwner()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if u == nil {
		response.NotFound(c)
		return
	}
	if !middleware.IsAuthenticated(c) {
		response.OK(c, toPublicResponse(u))
		return
	}
	response.OK(c, toResponse(u))
}

func (h *Handler) updateProfile(c *gin.Context) {
	userID := middleware.CurrentUserID(c)
	var dto UpdateUserDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	u, err := h.svc.UpdateProfile(userID, &dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if u == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, toResponse(u))
}

func (h *Handler) logout(c *gin.Context) {
	sessionID := middleware.CurrentSessionID(c)
	if sessionID != "" {
		_ = sessionpkg.Revoke(h.svc.db, middleware.CurrentUserID(c), sessionID)
	}
	response.NoContent(c)
}

func (h *Handler) loginWithToken(c *gin.Context) {
	userID := middleware.CurrentUserID(c)
	if userID == "" {
		response.Unauthorized(c)
		return
	}
	currentSessionID := middleware.CurrentSessionID(c)
	token, _, err := sessionpkg.Issue(h.svc.db, userID, c.ClientIP(), c.Request.UserAgent(), sessionpkg.DefaultTTL)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if currentSessionID != "" {
		sessionpkg.RevokeAfter(h.svc.db, userID, currentSessionID, 6*time.Second)
	}
	response.OK(c, gin.H{"token": token})
}

func (h *Handler) changePassword(c *gin.Context) {
	userID := middleware.CurrentUserID(c)
	var dto ChangePasswordDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.svc.ChangePassword(userID, dto.OldPassword, dto.NewPassword); err != nil {
		if errors.Is(err, errWrongPassword) {
			response.BadRequest(c, "密码不正确")
			return
		}
		if errors.Is(err, errPasswordSameAsOld) {
			response.UnprocessableEntity(c, "密码可不能和原来的一样哦")
			return
		}
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) listSessions(c *gin.Context) {
	userID := middleware.CurrentUserID(c)
	currentSessionID := middleware.CurrentSessionID(c)

	sessions, err := sessionpkg.ListActive(h.svc.db, userID)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	data := make([]gin.H, 0, len(sessions))
	for _, s := range sessions {
		data = append(data, gin.H{
			"id":      s.ID,
			"ua":      s.UA,
			"ip":      s.IP,
			"date":    s.UpdatedAt,
			"current": s.ID == currentSessionID,
		})
	}
	if len(data) == 0 {
		var u models.UserModel
		if err := h.svc.db.Select("id, last_login_time, last_login_ip").First(&u, "id = ?", userID).Error; err == nil {
			legacyDate := time.Now()
			if u.LastLoginTime != nil {
				legacyDate = *u.LastLoginTime
			}
			data = append(data, gin.H{
				"id":      "legacy-current",
				"ua":      c.Request.UserAgent(),
				"ip":      u.LastLoginIP,
				"date":    legacyDate,
				"current": true,
			})
		}
	}

	response.OK(c, gin.H{"data": data})
}

func (h *Handler) deleteSession(c *gin.Context) {
	userID := middleware.CurrentUserID(c)
	sessionID := c.Param("tokenId")
	if err := sessionpkg.Revoke(h.svc.db, userID, sessionID); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) deleteAllSessions(c *gin.Context) {
	userID := middleware.CurrentUserID(c)
	if err := sessionpkg.RevokeAllExcept(h.svc.db, userID, middleware.CurrentSessionID(c)); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}
