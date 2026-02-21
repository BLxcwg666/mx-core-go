package user

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	appconfigs "github.com/mx-space/core/internal/modules/configs"
	jwtpkg "github.com/mx-space/core/internal/pkg/jwt"
	"github.com/mx-space/core/internal/pkg/response"
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

func (s *Service) Login(username, password, ip string) (string, *models.UserModel, error) {
	var u models.UserModel
	if err := s.db.Select("id, username, name, avatar, password, mail, url, introduce, social_ids, last_login_time, last_login_ip").
		Where("username = ?", username).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil, fmt.Errorf("user not found")
		}
		return "", nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err != nil {
		return "", nil, fmt.Errorf("wrong password")
	}
	now := time.Now()
	s.db.Model(&u).Updates(map[string]interface{}{
		"last_login_time": now,
		"last_login_ip":   ip,
	})
	u.LastLoginTime = &now
	u.LastLoginIP = ip

	token, err := jwtpkg.Sign(u.ID, 30*24*time.Hour)
	return token, &u, err
}

func (s *Service) Register(dto *RegisterDTO) (*models.UserModel, error) {
	var count int64
	s.db.Model(&models.UserModel{}).Count(&count)
	if count > 0 {
		return nil, fmt.Errorf("owner already registered")
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
		return fmt.Errorf("wrong password")
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
			_, err := jwtpkg.Parse(token)
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
	if h.cfgSvc != nil {
		if cfg, err := h.cfgSvc.Get(); err == nil && cfg != nil {
			passwordEnabled = !cfg.AuthSecurity.DisablePasswordLogin
		}
	}

	response.OK(c, gin.H{
		"password": passwordEnabled,
		"passkey":  passkeyCount > 0,
	})
}

func (h *Handler) login(c *gin.Context) {
	var dto LoginDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	token, u, err := h.svc.Login(dto.Username, dto.Password, c.ClientIP())
	if err != nil {
		response.UnprocessableEntity(c, err.Error())
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
		if err.Error() == "owner already registered" {
			response.Forbidden(c)
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
	// JWT is stateless; client discards the token.
	// If using API tokens, the client should call DELETE /auth/token.
	response.NoContent(c)
}

func (h *Handler) loginWithToken(c *gin.Context) {
	userID := middleware.CurrentUserID(c)
	if userID == "" {
		response.Unauthorized(c)
		return
	}
	token, err := jwtpkg.Sign(userID, 30*24*time.Hour)
	if err != nil {
		response.InternalError(c, err)
		return
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
		if err.Error() == "wrong password" {
			response.BadRequest(c, err.Error())
			return
		}
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) listSessions(c *gin.Context) {
	response.OK(c, gin.H{"data": []interface{}{}})
}

func (h *Handler) deleteSession(c *gin.Context) {
	response.NoContent(c)
}

func (h *Handler) deleteAllSessions(c *gin.Context) {
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
