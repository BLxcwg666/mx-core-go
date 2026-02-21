package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/response"
	sessionpkg "github.com/mx-space/core/internal/pkg/session"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type LoginDTO struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type RegisterDTO struct {
	Username string `json:"username" binding:"required,min=3"`
	Password string `json:"password" binding:"required,min=6"`
	Name     string `json:"name"`
}

type CreateTokenDTO struct {
	Name      string     `json:"name"       binding:"required"`
	Expired   *time.Time `json:"expired"`
	ExpiredAt *time.Time `json:"expired_at"`
}

type loginResponse struct {
	Token string `json:"token"`
}

type tokenResponse struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Token   string     `json:"token"`
	Expired *time.Time `json:"expired"`
	Created time.Time  `json:"created"`
}

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) Login(username, password, ip, ua string) (string, error) {
	var u models.UserModel
	if err := s.db.Select("id, password").
		Where("username = ?", username).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", fmt.Errorf("user not found")
		}
		return "", err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err != nil {
		return "", fmt.Errorf("wrong password")
	}
	token, _, err := sessionpkg.Issue(s.db, u.ID, ip, ua, sessionpkg.DefaultTTL)
	return token, err
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

func (s *Service) ListTokens(userID string) ([]models.APIToken, error) {
	var tokens []models.APIToken
	return tokens, s.db.Where("user_id = ? AND (expired_at IS NULL OR expired_at > ?)", userID, time.Now()).
		Order("created_at DESC").Find(&tokens).Error
}

func (s *Service) GetToken(userID, tokenID string) (*models.APIToken, error) {
	var t models.APIToken
	if err := s.db.Where("id = ? AND user_id = ? AND (expired_at IS NULL OR expired_at > ?)", tokenID, userID, time.Now()).
		First(&t).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (s *Service) VerifyTokenString(token string) (bool, error) {
	var count int64
	err := s.db.Model(&models.APIToken{}).
		Where("token = ? AND (expired_at IS NULL OR expired_at > ?)", token, time.Now()).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Service) CreateToken(userID string, dto *CreateTokenDTO) (*models.APIToken, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	token := "txo" + hex.EncodeToString(b)

	t := models.APIToken{
		UserID:    userID,
		Token:     token,
		Name:      dto.Name,
		ExpiredAt: firstNonNilTime(dto.Expired, dto.ExpiredAt),
	}
	return &t, s.db.Create(&t).Error
}

func (s *Service) DeleteToken(userID, tokenID string) error {
	result := s.db.Where("id = ? AND user_id = ?", tokenID, userID).
		Delete(&models.APIToken{})
	if result.RowsAffected == 0 {
		return fmt.Errorf("token not found")
	}
	return result.Error
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

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
	token, err := h.svc.Login(dto.Username, dto.Password, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		response.UnprocessableEntity(c, err.Error())
		return
	}
	response.OK(c, loginResponse{Token: token})
}

func (h *Handler) signInUsername(c *gin.Context) {
	var dto LoginDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	token, err := h.svc.Login(dto.Username, dto.Password, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		response.UnprocessableEntity(c, err.Error())
		return
	}
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
		if err.Error() == "owner already registered" {
			response.Forbidden(c)
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
			response.NotFound(c)
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
	response.OK(c, gin.H{
		"user": gin.H{
			"id": userID,
		},
		"is_owner": true,
	})
}

func (h *Handler) signOut(c *gin.Context) {
	response.OK(c, gin.H{"success": true})
}

func (h *Handler) getAuthSession(c *gin.Context) {
	if !middleware.IsAuthenticated(c) {
		response.OK(c, nil)
		return
	}
	userID := middleware.CurrentUserID(c)
	response.OK(c, gin.H{
		"user": gin.H{"id": userID},
		"session": gin.H{
			"userId": userID,
		},
	})
}

func (h *Handler) listAuthSessions(c *gin.Context) {
	response.OK(c, []interface{}{})
}

func (h *Handler) revokeSession(c *gin.Context) {
	response.OK(c, gin.H{"status": true})
}

func (h *Handler) revokeSessions(c *gin.Context) {
	response.OK(c, gin.H{"status": true})
}

func (h *Handler) revokeOtherSessions(c *gin.Context) {
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
		response.NotFoundMsg(c, "reader not found")
		return
	}
	response.OK(c, gin.H{"status": true})
}

func firstNonNilTime(values ...*time.Time) *time.Time {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}
