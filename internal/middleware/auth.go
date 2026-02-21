package middleware

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/pkg/jwt"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

const (
	ContextKeyUserID = "user_id"
	apiTokenPrefix   = "txo"
)

// Auth returns a middleware that enforces JWT or API token authentication.
func Auth(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := ValidateToken(db, extractToken(c))
		if err != nil {
			response.Unauthorized(c)
			return
		}
		c.Set(ContextKeyUserID, userID)
		c.Next()
	}
}

// OptionalAuth sets the user ID if a valid token is present, but does not block the request.
func OptionalAuth(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if userID, err := ValidateToken(db, extractToken(c)); err == nil && userID != "" {
			c.Set(ContextKeyUserID, userID)
		}
		c.Next()
	}
}

// ValidateToken validates JWT/API token and returns the authenticated user id.
func ValidateToken(db *gorm.DB, rawToken string) (string, error) {
	token := NormalizeToken(rawToken)
	if token == "" {
		return "", errors.New("token is required")
	}

	if strings.HasPrefix(token, apiTokenPrefix) {
		return validateAPIToken(db, token)
	}

	claims, err := jwt.Parse(token)
	if err != nil {
		return "", err
	}
	return claims.UserID, nil
}

// CurrentUserID extracts the authenticated user ID from context.
func CurrentUserID(c *gin.Context) string {
	v, _ := c.Get(ContextKeyUserID)
	id, _ := v.(string)
	return id
}

// IsAuthenticated returns true if the request has a valid auth token.
func IsAuthenticated(c *gin.Context) bool {
	return CurrentUserID(c) != ""
}

func extractToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if auth != "" {
		return NormalizeToken(auth)
	}
	return NormalizeToken(c.Query("token"))
}

// NormalizeToken trims spaces and strips optional Bearer prefix.
func NormalizeToken(raw string) string {
	token := strings.TrimSpace(raw)
	if token == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		return strings.TrimSpace(token[7:])
	}
	return token
}

func validateAPIToken(db *gorm.DB, token string) (string, error) {
	var row struct {
		UserID string
	}
	err := db.Table("api_tokens").
		Select("user_id").
		Where("token = ? AND (expired_at IS NULL OR expired_at > NOW()) AND deleted_at IS NULL", token).
		Scan(&row).Error
	if err != nil {
		return "", err
	}
	if row.UserID == "" {
		return "", errors.New("api token not found")
	}
	return row.UserID, nil
}
