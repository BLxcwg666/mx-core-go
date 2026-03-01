package auth

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
	jwtpkg "github.com/mx-space/core/internal/pkg/jwt"
	"github.com/mx-space/core/internal/pkg/response"
	sessionpkg "github.com/mx-space/core/internal/pkg/session"
	"gorm.io/gorm"
)

type Handler struct {
	svc    *Service
	cfgSvc *appconfigs.Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{
		svc:    svc,
		cfgSvc: appconfigs.NewService(svc.db),
	}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	a := rg.Group("/auth")

	a.POST("/login", h.login)
	a.POST("/sign-in/username", h.signInUsername) // Better Auth compatibility
	a.POST("/register", h.register)
	a.POST("/sign-out", h.signOut) // Better Auth compatibility
	a.GET("/get-session", middleware.OptionalAuth(h.svc.db), h.getAuthSession)
	a.GET("/list-sessions", authMW, h.listAuthSessions)
	a.POST("/revoke-session", authMW, h.revokeSession)
	a.POST("/revoke-sessions", authMW, h.revokeSessions)
	a.POST("/revoke-other-sessions", authMW, h.revokeOtherSessions)
	a.GET("/session", middleware.OptionalAuth(h.svc.db), h.session)
	a.PATCH("/as-owner", authMW, h.asOwner)

	tok := a.Group("/token", authMW)
	tok.GET("", h.listTokens)
	tok.POST("", h.createToken)
	tok.DELETE("", h.deleteTokenByQuery) // legacy compatibility: DELETE /auth/token?id=...
	tok.DELETE("/:id", h.deleteToken)
}

func (h *Handler) login(c *gin.Context) {
	var dto LoginDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	disabled, err := h.isPasswordLoginDisabled()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if disabled {
		response.BadRequest(c, "密码登录已禁用")
		return
	}
	token, err := h.svc.Login(dto.Username, dto.Password, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		if errors.Is(err, errAuthUserNotFound) {
			response.ForbiddenMsg(c, "用户名不正确")
			return
		}
		if errors.Is(err, errAuthWrongPassword) {
			response.ForbiddenMsg(c, "密码不正确")
			return
		}
		response.InternalError(c, err)
		return
	}
	setAuthTokenCookie(c, token)
	response.OK(c, loginResponse{Token: token})
}

func (h *Handler) signInUsername(c *gin.Context) {
	var dto LoginDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	disabled, err := h.isPasswordLoginDisabled()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if disabled {
		response.BadRequest(c, "密码登录已禁用")
		return
	}
	token, err := h.svc.Login(dto.Username, dto.Password, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		if errors.Is(err, errAuthUserNotFound) {
			response.ForbiddenMsg(c, "用户名不正确")
			return
		}
		if errors.Is(err, errAuthWrongPassword) {
			response.ForbiddenMsg(c, "密码不正确")
			return
		}
		response.InternalError(c, err)
		return
	}
	setAuthTokenCookie(c, token)
	response.OK(c, gin.H{
		"token":   token,
		"success": true,
	})
}

func (h *Handler) register(c *gin.Context) {
	var dto RegisterDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	u, err := h.svc.Register(&dto)
	if err != nil {
		if errors.Is(err, errOwnerAlreadyRegistered) {
			response.BadRequest(c, "我已经有一个主人了哦")
			return
		}
		response.InternalError(c, err)
		return
	}
	response.Created(c, gin.H{"id": u.ID, "username": u.Username})
}

func (h *Handler) listTokens(c *gin.Context) {
	if token := strings.TrimSpace(c.Query("token")); token != "" {
		ok, err := h.svc.VerifyTokenString(token)
		if err != nil {
			response.InternalError(c, err)
			return
		}
		response.OK(c, ok)
		return
	}

	if tokenID := strings.TrimSpace(c.Query("id")); tokenID != "" {
		t, err := h.svc.GetToken(middleware.CurrentUserID(c), tokenID)
		if err != nil {
			response.InternalError(c, err)
			return
		}
		if t == nil {
			response.NotFoundMsg(c, "Token 不存在")
			return
		}
		response.OK(c, tokenResponse{
			ID:      t.ID,
			Name:    t.Name,
			Token:   t.Token,
			Expired: t.ExpiredAt,
			Created: t.CreatedAt,
		})
		return
	}

	tokens, err := h.svc.ListTokens(middleware.CurrentUserID(c))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	items := make([]tokenResponse, len(tokens))
	for i, t := range tokens {
		items[i] = tokenResponse{
			ID: t.ID, Name: t.Name, Token: t.Token,
			Expired: t.ExpiredAt, Created: t.CreatedAt,
		}
	}
	response.OK(c, gin.H{"data": items})
}

func (h *Handler) createToken(c *gin.Context) {
	var dto CreateTokenDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	t, err := h.svc.CreateToken(middleware.CurrentUserID(c), &dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, tokenResponse{
		ID: t.ID, Name: t.Name, Token: t.Token,
		Expired: t.ExpiredAt, Created: t.CreatedAt,
	})
}

func (h *Handler) deleteToken(c *gin.Context) {
	if err := h.svc.DeleteToken(middleware.CurrentUserID(c), c.Param("id")); err != nil {
		response.NotFoundMsg(c, err.Error())
		return
	}
	response.NoContent(c)
}

func (h *Handler) deleteTokenByQuery(c *gin.Context) {
	tokenID := c.Query("id")
	if tokenID == "" {
		response.BadRequest(c, "id is required")
		return
	}
	if err := h.svc.DeleteToken(middleware.CurrentUserID(c), tokenID); err != nil {
		response.NotFoundMsg(c, err.Error())
		return
	}
	response.NoContent(c)
}

func (h *Handler) session(c *gin.Context) {
	if !middleware.IsAuthenticated(c) {
		response.OK(c, nil)
		return
	}
	userID := middleware.CurrentUserID(c)
	user, err := h.loadUserSessionProfile(userID)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if user == nil {
		response.OK(c, nil)
		return
	}

	account := h.loadLatestOAuthAccount(userID)
	provider := account.Provider
	providerAccountID := strings.TrimSpace(account.ProviderUID)
	if providerAccountID == "" {
		providerAccountID = user.ID
	}
	accountType := "credential"
	if provider != "" {
		accountType = "oauth"
	}

	out := gin.H{
		"id":                user.ID,
		"name":              displayName(user.Name, user.Username),
		"email":             user.Mail,
		"image":             user.Avatar,
		"handle":            user.Username,
		"isOwner":           true,
		"providerAccountId": providerAccountID,
		"provider":          provider,
		"providerId":        provider,
		"type":              accountType,
		"userId":            user.ID,
	}
	response.OK(c, out)
}

func (h *Handler) signOut(c *gin.Context) {
	if token := extractAuthTokenFromRequest(c); token != "" {
		if claims, err := jwtpkg.Parse(token); err == nil {
			sessionID := strings.TrimSpace(claims.SessionID)
			userID := strings.TrimSpace(claims.UserID)
			if sessionID != "" && userID != "" {
				_ = sessionpkg.Revoke(h.svc.db, userID, sessionID)
			}
		}
	}
	clearAuthTokenCookie(c)
	response.OK(c, gin.H{"success": true})
}

func (h *Handler) getAuthSession(c *gin.Context) {
	if !middleware.IsAuthenticated(c) {
		response.OK(c, nil)
		return
	}
	userID := middleware.CurrentUserID(c)
	sessionID := middleware.CurrentSessionID(c)
	if sessionID == "" {
		response.OK(c, nil)
		return
	}

	user, err := h.loadUserSessionProfile(userID)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if user == nil {
		response.OK(c, nil)
		return
	}

	var s models.UserSession
	if err := h.svc.db.Where("id = ? AND user_id = ?", sessionID, userID).First(&s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.OK(c, nil)
			return
		}
		response.InternalError(c, err)
		return
	}

	rawToken := extractAuthTokenFromRequest(c)
	if rawToken == "" {
		rawToken = s.ID
	}

	response.OK(c, gin.H{
		"session": gin.H{
			"id":        s.ID,
			"token":     rawToken,
			"userId":    userID,
			"expiresAt": s.ExpiresAt,
			"createdAt": s.CreatedAt,
			"updatedAt": s.UpdatedAt,
			"ipAddress": s.IP,
			"userAgent": s.UA,
		},
		"user": gin.H{
			"id":            user.ID,
			"name":          displayName(user.Name, user.Username),
			"email":         user.Mail,
			"image":         user.Avatar,
			"emailVerified": true,
			"createdAt":     user.CreatedAt,
			"updatedAt":     user.UpdatedAt,
		},
	})
}

func (h *Handler) listAuthSessions(c *gin.Context) {
	userID := middleware.CurrentUserID(c)
	sessions, err := sessionpkg.ListActive(h.svc.db, userID)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	items := make([]gin.H, 0, len(sessions))
	for _, s := range sessions {
		items = append(items, gin.H{
			"id":        s.ID,
			"token":     s.ID,
			"userId":    s.UserID,
			"expiresAt": s.ExpiresAt,
			"createdAt": s.CreatedAt,
			"updatedAt": s.UpdatedAt,
			"ipAddress": s.IP,
			"userAgent": s.UA,
		})
	}
	c.JSON(200, items)
}

func (h *Handler) revokeSession(c *gin.Context) {
	var body struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	sessionID := resolveSessionIDFromToken(body.Token)
	if sessionID != "" {
		err := sessionpkg.Revoke(h.svc.db, middleware.CurrentUserID(c), sessionID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			response.InternalError(c, err)
			return
		}
	}
	response.OK(c, gin.H{"status": true})
}

func (h *Handler) revokeSessions(c *gin.Context) {
	if err := sessionpkg.RevokeAllExcept(h.svc.db, middleware.CurrentUserID(c), ""); err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, gin.H{"status": true})
}

func (h *Handler) revokeOtherSessions(c *gin.Context) {
	if err := sessionpkg.RevokeAllExcept(h.svc.db, middleware.CurrentUserID(c), middleware.CurrentSessionID(c)); err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, gin.H{"status": true})
}

func (h *Handler) asOwner(c *gin.Context) {
	var body struct {
		ID     string `json:"id"`
		Email  string `json:"email"`
		Handle string `json:"handle"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		// Keep compatibility with clients that call this endpoint with empty body.
		// We'll fallback to query parameters below.
	}

	id := body.ID
	if id == "" {
		id = c.Query("id")
	}
	email := body.Email
	if email == "" {
		email = c.Query("email")
	}
	handle := body.Handle
	if handle == "" {
		handle = c.Query("handle")
	}

	query := h.svc.db.Model(&models.ReaderModel{})
	switch {
	case id != "":
		query = query.Where("id = ?", id)
	case email != "":
		query = query.Where("email = ?", email)
	case handle != "":
		query = query.Where("handle = ?", handle)
	default:
		response.OK(c, gin.H{"status": true})
		return
	}

	res := query.Update("is_owner", true)
	if res.Error != nil {
		response.InternalError(c, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		response.NotFoundMsg(c, "读者不存在")
		return
	}
	response.OK(c, gin.H{"status": true})
}

func (h *Handler) loadUserSessionProfile(userID string) (*models.UserModel, error) {
	var u models.UserModel
	if err := h.svc.db.
		Select("id, username, name, avatar, mail, created_at, updated_at").
		Where("id = ?", userID).
		First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (h *Handler) loadLatestOAuthAccount(userID string) *models.OAuth2Token {
	var account models.OAuth2Token
	if err := h.svc.db.
		Where("user_id = ?", userID).
		Order("last_used DESC, updated_at DESC, created_at DESC").
		First(&account).Error; err != nil {
		return &models.OAuth2Token{}
	}
	return &account
}

func (h *Handler) isPasswordLoginDisabled() (bool, error) {
	if h.cfgSvc == nil {
		return false, nil
	}
	cfg, err := h.cfgSvc.Get()
	if err != nil {
		return false, err
	}
	if cfg == nil {
		return false, nil
	}
	return cfg.AuthSecurity.DisablePasswordLogin, nil
}
