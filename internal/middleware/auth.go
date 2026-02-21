package middleware

import (
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
		token := extractToken(c)
		if token == "" {
			response.Unauthorized(c)
			return
		}

		if strings.HasPrefix(token, apiTokenPrefix) {
			userID, err := validateAPIToken(db, token)
			if err != nil {
				response.Unauthorized(c)
				return
			}
			c.Set(ContextKeyUserID, userID)
			c.Next()
			return
		}

		claims, err := jwt.Parse(token)
		if err != nil {
			response.Unauthorized(c)
			return
		}

		c.Set(ContextKeyUserID, claims.UserID)
		c.Next()
	}
}

// OptionalAuth sets the user ID if a valid token is present, but does not block the request.
func OptionalAuth(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token != "" {
			if strings.HasPrefix(token, apiTokenPrefix) {
				if userID, err := validateAPIToken(db, token); err == nil {
					c.Set(ContextKeyUserID, userID)
				}
			} else {
				if claims, err := jwt.Parse(token); err == nil {
					c.Set(ContextKeyUserID, claims.UserID)
				}
			}
		}
		c.Next()
	}
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
	auth := strings.TrimSpace(c.GetHeader("Authorization"))
	if auth != "" {
		lowerAuth := strings.ToLower(auth)
		if strings.HasPrefix(lowerAuth, "bearer ") {
			return strings.TrimSpace(auth[7:])
		}
		return auth
	}
	return c.Query("token")
}

func validateAPIToken(db *gorm.DB, token string) (string, error) {
	var row struct {
		UserID string
	}
	err := db.Table("api_tokens").
		Select("user_id").
		Where("token = ? AND (expired_at IS NULL OR expired_at > NOW()) AND deleted_at IS NULL", token).
		Scan(&row).Error
	if err != nil || row.UserID == "" {
		return "", err
	}
	return row.UserID, nil
}
