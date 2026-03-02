package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	idempotenceHeader = "x-idempotence"
	idempotenceTTL    = 60 * time.Second
)

type idempotenceRouteRule struct {
	Method  string
	Pattern string
}

var idempotenceRouteRules = []idempotenceRouteRule{
	{Method: http.MethodPost, Pattern: "/api/v2/categories"},
	{Method: http.MethodPost, Pattern: "/api/v2/notes"},
	{Method: http.MethodPost, Pattern: "/api/v2/pages"},
	{Method: http.MethodPost, Pattern: "/api/v2/posts"},
	{Method: http.MethodPost, Pattern: "/api/v2/topics"},
	{Method: http.MethodPost, Pattern: "/api/v2/snippets"},
	{Method: http.MethodPost, Pattern: "/api/v2/says"},
	{Method: http.MethodPost, Pattern: "/api/v2/projects"},
	{Method: http.MethodPost, Pattern: "/api/v2/recently"},
	{Method: http.MethodPost, Pattern: "/api/v2/shorthand"},
	{Method: http.MethodPost, Pattern: "/api/v2/links"},
	{Method: http.MethodPost, Pattern: "/api/v2/friends"},
	{Method: http.MethodPost, Pattern: "/api/v2/links/audit"},
	{Method: http.MethodPost, Pattern: "/api/v2/friends/audit"},
	{Method: http.MethodPost, Pattern: "/api/v2/comments/:id"},
	{Method: http.MethodPost, Pattern: "/api/v2/comments/reply/:id"},
	{Method: http.MethodPost, Pattern: "/api/v2/comments/master/comment/:id"},
	{Method: http.MethodPost, Pattern: "/api/v2/comments/master/reply/:id"},
	{Method: http.MethodPost, Pattern: "/api/v2/comments/owner/comment/:id"},
	{Method: http.MethodPost, Pattern: "/api/v2/comments/owner/reply/:id"},
	{Method: http.MethodPost, Pattern: "/api/v2/webhooks/redispatch/:id"},
}

// Idempotence returns a middleware that prevents duplicate requests for selected routes.
func Idempotence(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !shouldApplyIdempotence(c.Request.Method, c.Request.URL.Path) {
			c.Next()
			return
		}

		key, err := resolveIdempotenceKey(c)
		if err != nil || key == "" {
			c.Next()
			return
		}

		redisKey := fmt.Sprintf("mx:idempotence:%s", key)
		ctx := c.Request.Context()

		val, err := rdb.Get(ctx, redisKey).Result()
		if err == nil {
			msg := "相同请求成功后在 60 秒内只能发送一次"
			if val == "0" {
				msg = "相同请求正在处理中..."
			}
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{
				"ok":      0,
				"code":    http.StatusConflict,
				"message": msg,
			})
			return
		}

		if !errors.Is(err, redis.Nil) {
			c.Next()
			return
		}

		if setErr := rdb.Set(ctx, redisKey, "0", idempotenceTTL).Err(); setErr != nil {
			c.Next()
			return
		}

		c.Next()

		status := c.Writer.Status()
		if status >= 200 && status < 300 {
			rdb.Set(ctx, redisKey, "1", redis.KeepTTL)
		} else {
			rdb.Del(ctx, redisKey)
		}
	}
}

func shouldApplyIdempotence(method, path string) bool {
	normalizedMethod := strings.ToUpper(strings.TrimSpace(method))
	normalizedPath := normalizeIdempotencePath(path)

	for _, rule := range idempotenceRouteRules {
		if normalizedMethod != rule.Method {
			continue
		}
		if idempotencePatternMatch(rule.Pattern, normalizedPath) {
			return true
		}
	}
	return false
}

func normalizeIdempotencePath(path string) string {
	p := strings.TrimSpace(strings.ToLower(path))
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = strings.TrimRight(p, "/")
	if p == "" {
		return "/"
	}
	return p
}

func idempotencePatternMatch(pattern, path string) bool {
	pattern = normalizeIdempotencePath(pattern)
	path = normalizeIdempotencePath(path)

	if pattern == path {
		return true
	}

	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	if len(patternParts) != len(pathParts) {
		return false
	}

	for i := range patternParts {
		segment := patternParts[i]
		if strings.HasPrefix(segment, ":") && len(segment) > 1 {
			continue
		}
		if segment != pathParts[i] {
			return false
		}
	}
	return true
}

// resolveIdempotenceKey returns the idempotence key for the current request.
func resolveIdempotenceKey(c *gin.Context) (string, error) {
	if hdr := c.GetHeader(idempotenceHeader); hdr != "" {
		return hdr, nil
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return "", err
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

	ua := c.Request.UserAgent()
	ip := c.ClientIP()
	authToken := resolveIdempotenceAuthToken(c)

	if len(body) == 0 && ua == "" && ip == "" && authToken == "" {
		return "", nil
	}

	raw := c.Request.Method + "|" + c.Request.URL.String() + "|" + string(body) + "|" + ua + "|" + ip + "|" + authToken
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:]), nil
}

func resolveIdempotenceAuthToken(c *gin.Context) string {
	if token := NormalizeToken(c.GetHeader("Authorization")); token != "" {
		return token
	}
	if token := NormalizeToken(c.Query("token")); token != "" {
		return token
	}
	for _, cookieKey := range []string{"mx-token", "mx_token", "token"} {
		if raw, err := c.Cookie(cookieKey); err == nil {
			if token := NormalizeToken(raw); token != "" {
				return token
			}
		}
	}
	return ""
}
