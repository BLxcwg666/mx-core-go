package analyze

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"gorm.io/gorm"
)

// Middleware records each non-admin, non-bot public GET request as an analytics event.
func Middleware(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next() // handle request first to get status code

		// Track successful public GET requests.
		if c.Request.Method != "GET" {
			return
		}
		rawPath := strings.TrimSpace(c.Request.URL.Path)
		if rawPath != "/api" && !strings.HasPrefix(rawPath, "/api/") {
			return
		}
		path := normalizeAnalyzePath(rawPath)

		// Skip proxy paths
		if strings.HasPrefix(path, "/proxy") {
			return
		}
		if c.Writer.Status() < 200 || c.Writer.Status() >= 300 {
			return
		}

		// Skip bot user-agents
		if isBotUA(c.GetHeader("User-Agent")) {
			return
		}

		// Skip authenticated users (has Authorization header)
		if c.GetHeader("Authorization") != "" {
			return
		}

		ip := strings.TrimSpace(c.ClientIP())
		if ip == "" || ip == "127.0.0.1" || ip == "localhost" || ip == "::1" {
			return
		}

		ua := parseUA(c.GetHeader("User-Agent"))
		referer := c.GetHeader("Referer")

		go func() {
			_ = db.Create(&models.AnalyzeModel{
				IP:        ip,
				UA:        ua,
				Path:      path,
				Referer:   referer,
				Timestamp: time.Now(),
			}).Error
		}()
	}
}

// isBotUA returns true if the User-Agent string indicates a bot/crawler.
func isBotUA(ua string) bool {
	lower := strings.ToLower(ua)
	botKeywords := []string{"bot", "crawler", "spider", "headless", "wget", "curl", "python-requests", "go-http", "java/", "scrapy"}
	for _, kw := range botKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// normalizeAnalyzePath strips the /api and optional /vN version prefix.
func normalizeAnalyzePath(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return "/"
	}

	if p == "/api" {
		return "/"
	}
	if strings.HasPrefix(p, "/api/") {
		p = strings.TrimPrefix(p, "/api")
	}
	if strings.HasPrefix(p, "/v") {
		rest := p[2:]
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			if isDigits(rest[:slash]) {
				p = rest[slash:]
			}
		} else if isDigits(rest) {
			return "/"
		}
	}

	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

// isDigits returns true when raw is a non-empty all-digit string.
func isDigits(raw string) bool {
	if raw == "" {
		return false
	}
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// parseUA extracts browser, OS, and device-type information from a UA string.
func parseUA(ua string) map[string]interface{} {
	result := map[string]interface{}{
		"ua":      ua,
		"raw":     ua,
		"type":    "desktop",
		"browser": map[string]interface{}{"name": "Unknown"},
		"os":      map[string]interface{}{"name": "Unknown"},
	}
	lower := strings.ToLower(ua)

	switch {
	case strings.Contains(lower, "edg/"):
		result["browser"] = map[string]interface{}{"name": "Edge"}
	case strings.Contains(lower, "chrome/"):
		result["browser"] = map[string]interface{}{"name": "Chrome"}
	case strings.Contains(lower, "safari/") && strings.Contains(lower, "version/"):
		result["browser"] = map[string]interface{}{"name": "Safari"}
	case strings.Contains(lower, "firefox/"):
		result["browser"] = map[string]interface{}{"name": "Firefox"}
	}

	switch {
	case strings.Contains(lower, "windows"):
		result["os"] = map[string]interface{}{"name": "Windows"}
	case strings.Contains(lower, "mac os"):
		result["os"] = map[string]interface{}{"name": "macOS"}
	case strings.Contains(lower, "android"):
		result["os"] = map[string]interface{}{"name": "Android"}
	case strings.Contains(lower, "iphone") || strings.Contains(lower, "ipad") || strings.Contains(lower, "ios"):
		result["os"] = map[string]interface{}{"name": "iOS"}
	case strings.Contains(lower, "linux"):
		result["os"] = map[string]interface{}{"name": "Linux"}
	}

	switch {
	case strings.Contains(lower, "bot") || strings.Contains(lower, "crawler") || strings.Contains(lower, "spider"):
		result["type"] = "bot"
	case strings.Contains(lower, "tablet") || strings.Contains(lower, "ipad"):
		result["type"] = "tablet"
	case strings.Contains(lower, "mobile"):
		result["type"] = "mobile"
	default:
		result["type"] = "desktop"
	}
	return result
}
