package user

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	appconfigs "github.com/mx-space/core/internal/modules/configs"
	"github.com/mx-space/core/internal/pkg/response"
	sessionpkg "github.com/mx-space/core/internal/pkg/session"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type UpdateUserDTO struct {
	Name         *string                 `json:"name"`
	Introduce    *string                 `json:"introduce"`
	Avatar       *string                 `json:"avatar"`
	Mail         *string                 `json:"mail"`
	URL          *string                 `json:"url"`
	SocialIDs    *map[string]interface{} `json:"social_ids"`
	SocialIDsAlt *map[string]interface{} `json:"socialIds"`
}

type ChangePasswordDTO struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6"`
}

type LoginDTO struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type RegisterDTO struct {
	Username string `json:"username" binding:"required,min=3"`
	Password string `json:"password" binding:"required,min=6"`
	Name     string `json:"name"`
}

type userResponse struct {
	ID            string                 `json:"id"`
	Username      string                 `json:"username"`
	Name          string                 `json:"name"`
	Introduce     string                 `json:"introduce"`
	Avatar        string                 `json:"avatar"`
	Mail          string                 `json:"mail"`
	URL           string                 `json:"url"`
	SocialIDs     map[string]interface{} `json:"social_ids"`
	LastLoginTime *time.Time             `json:"last_login_time"`
	LastLoginIP   string                 `json:"last_login_ip"`
}

type publicUserResponse struct {
	ID        string                 `json:"id"`
	Username  string                 `json:"username"`
	Name      string                 `json:"name"`
	Introduce string                 `json:"introduce"`
	Avatar    string                 `json:"avatar"`
	Mail      string                 `json:"mail"`
	URL       string                 `json:"url"`
	SocialIDs map[string]interface{} `json:"social_ids"`
}

type loginResponse struct {
	Token string        `json:"token"`
	User  *userResponse `json:"user,omitempty"`
}

var (
	errUserNotFound       = errors.New("user not found")
	errWrongPassword      = errors.New("wrong password")
	errOwnerAlreadyExists = errors.New("owner already registered")
	errPasswordSameAsOld  = errors.New("password same as old")
)

func toResponse(u *models.UserModel) *userResponse {
	return &userResponse{
		ID: u.ID, Username: u.Username, Name: u.Name,
		Introduce: u.Introduce, Avatar: u.Avatar, Mail: u.Mail, URL: u.URL,
		SocialIDs:     parseSocialIDs(u.SocialIDs),
		LastLoginTime: u.LastLoginTime, LastLoginIP: u.LastLoginIP,
	}
}

func toPublicResponse(u *models.UserModel) *publicUserResponse {
	return &publicUserResponse{
		ID: u.ID, Username: u.Username, Name: u.Name,
		Introduce: u.Introduce, Avatar: u.Avatar, Mail: u.Mail, URL: u.URL,
		SocialIDs: parseSocialIDs(u.SocialIDs),
	}
}

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) GetOwner() (*models.UserModel, error) {
	var u models.UserModel
	if err := s.db.First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (s *Service) GetByID(id string) (*models.UserModel, error) {
	var u models.UserModel
	if err := s.db.First(&u, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (s *Service) Login(username, password, ip, ua string) (string, *models.UserModel, error) {
	var u models.UserModel
	if err := s.db.Select("id, username, name, avatar, password, mail, url, introduce, social_ids, last_login_time, last_login_ip").
		Where("username = ?", username).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			time.Sleep(3 * time.Second)
			return "", nil, errUserNotFound
		}
		return "", nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err != nil {
		return "", nil, errWrongPassword
	}
	now := time.Now()
	s.db.Model(&u).Updates(map[string]interface{}{
		"last_login_time": now,
		"last_login_ip":   ip,
	})
	u.LastLoginTime = &now
	u.LastLoginIP = ip

	token, _, err := sessionpkg.Issue(s.db, u.ID, ip, ua, sessionpkg.DefaultTTL)
	return token, &u, err
}

func (s *Service) Register(dto *RegisterDTO) (*models.UserModel, error) {
	var count int64
	s.db.Model(&models.UserModel{}).Count(&count)
	if count > 0 {
		return nil, errOwnerAlreadyExists
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(dto.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	name := dto.Name
	if name == "" {
		name = dto.Username
	}
	u := models.UserModel{Username: dto.Username, Password: string(hash), Name: name}
	return &u, s.db.Create(&u).Error
}

func (s *Service) IsRegistered() bool {
	var count int64
	s.db.Model(&models.UserModel{}).Count(&count)
	return count > 0
}

func (s *Service) UpdateProfile(id string, dto *UpdateUserDTO) (*models.UserModel, error) {
	u, err := s.GetByID(id)
	if err != nil || u == nil {
		return u, err
	}
	updates := map[string]interface{}{}
	if dto.Name != nil {
		updates["name"] = *dto.Name
		u.Name = *dto.Name
	}
	if dto.Introduce != nil {
		updates["introduce"] = *dto.Introduce
		u.Introduce = *dto.Introduce
	}
	if dto.Avatar != nil {
		updates["avatar"] = *dto.Avatar
		u.Avatar = *dto.Avatar
	}
	if dto.Mail != nil {
		updates["mail"] = *dto.Mail
		u.Mail = *dto.Mail
	}
	if dto.URL != nil {
		updates["url"] = *dto.URL
		u.URL = *dto.URL
	}
	socialIDs := dto.SocialIDs
	if socialIDs == nil {
		socialIDs = dto.SocialIDsAlt
	}
	if socialIDs != nil {
		encoded, err := encodeSocialIDs(*socialIDs)
		if err != nil {
			return nil, err
		}
		updates["social_ids"] = encoded
		u.SocialIDs = encoded
	}
	return u, s.db.Model(u).Updates(updates).Error
}

func (s *Service) ChangePassword(id, oldPwd, newPwd string) error {
	var u models.UserModel
	if err := s.db.Select("id, password").First(&u, "id = ?", id).Error; err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(oldPwd)); err != nil {
		return errWrongPassword
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(newPwd)); err == nil {
		return errPasswordSameAsOld
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPwd), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.db.Model(&u).Update("password", string(hash)).Error
}

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

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func parseSocialIDs(raw string) map[string]interface{} {
	out := map[string]interface{}{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return map[string]interface{}{}
	}
	return out
}

func encodeSocialIDs(ids map[string]interface{}) (string, error) {
	if ids == nil {
		return "{}", nil
	}
	data, err := json.Marshal(ids)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func oauthProviderClientID(public map[string]interface{}, providerType string) string {
	if len(public) == 0 || strings.TrimSpace(providerType) == "" {
		return ""
	}
	raw, ok := public[providerType]
	if !ok {
		for k, v := range public {
			if strings.EqualFold(k, providerType) {
				raw = v
				ok = true
				break
			}
		}
		if !ok {
			return ""
		}
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return ""
	}
	for _, key := range []string{"client_id", "clientId"} {
		if value, ok := m[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
