package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/pkg/response"
	"go.uber.org/zap"
)

// ErrorReporter logs client-side error responses in a centralized way.
func ErrorReporter(log *zap.Logger) gin.HandlerFunc {
	if log == nil {
		log = zap.NewNop()
	}
	logger := log.Named("HTTPStatus")

	return func(c *gin.Context) {
		c.Next()

		status := c.Writer.Status()
		if status < http.StatusBadRequest || status >= http.StatusInternalServerError {
			return
		}
		if response.ErrorLogged(c) {
			return
		}

		fields := []zap.Field{
			zap.Int("status", status),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.RequestURI()),
			zap.String("ip", c.ClientIP()),
		}
		if route := c.FullPath(); route != "" {
			fields = append(fields, zap.String("route", route))
		}
		if message := strings.TrimSpace(response.ResponseMessage(c)); message != "" {
			fields = append(fields, zap.String("message", message))
		}
		if ua := strings.TrimSpace(c.GetHeader("User-Agent")); ua != "" {
			fields = append(fields, zap.String("ua", ua))
		}

		switch status {
		case http.StatusTooManyRequests:
			logger.Warn("request rate limited", fields...)
		case http.StatusUnauthorized, http.StatusForbidden:
			logger.Warn("request unauthorized", fields...)
		case http.StatusNotFound, http.StatusMethodNotAllowed:
			logger.Warn("request route rejected", fields...)
		default:
			logger.Warn("request rejected", fields...)
		}
	}
}
