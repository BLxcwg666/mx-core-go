package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// SessionCookieName is the HttpOnly cookie set by the OAuth callback. It must differ from "mx-token":
// the admin panel keeps its token in a script-managed "mx-token" cookie, and a browser refuses to let
// scripts overwrite an HttpOnly cookie of the same name, which locked admins out after an OAuth login.
const SessionCookieName = "mx-session"

const legacyTokenCookieName = "mx-token"

var authCookieNames = []string{legacyTokenCookieName, "mx_token", "token", SessionCookieName}

// TokenFromCookies returns the first auth token found in the known auth cookies.
func TokenFromCookies(c *gin.Context) string {
	for _, cookieKey := range authCookieNames {
		if raw, err := c.Cookie(cookieKey); err == nil {
			if token := NormalizeToken(raw); token != "" {
				return token
			}
		}
	}
	return ""
}

// ClearLegacyAuthCookie expires the HttpOnly "mx-token" cookie older versions set on OAuth login.
// Only call it on responses after which the admin panel writes its own token cookie again.
func ClearLegacyAuthCookie(c *gin.Context, secure bool) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(legacyTokenCookieName, "", -1, "/", "", secure, true)
}

func IsSecureRequest(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	proto := strings.TrimSpace(strings.Split(c.GetHeader("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(proto, "https")
}
