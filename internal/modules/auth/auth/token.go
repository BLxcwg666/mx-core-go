package auth

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	jwtpkg "github.com/mx-space/core/internal/pkg/jwt"
)

func extractAuthTokenFromRequest(c *gin.Context) string {
	if token := middleware.NormalizeToken(c.GetHeader("Authorization")); token != "" {
		return token
	}
	if token := middleware.NormalizeToken(c.Query("token")); token != "" {
		return token
	}
	for _, cookieKey := range []string{"mx-token", "mx_token", "token"} {
		if raw, err := c.Cookie(cookieKey); err == nil {
			if token := middleware.NormalizeToken(raw); token != "" {
				return token
			}
		}
	}
	return ""
}

func resolveSessionIDFromToken(rawToken string) string {
	token := middleware.NormalizeToken(rawToken)
	if token == "" {
		return ""
	}
	if claims, err := jwtpkg.Parse(token); err == nil {
		return strings.TrimSpace(claims.SessionID)
	}
	return strings.TrimSpace(token)
}
