package middleware

import (
	"errors"
	"net"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Recovery logs panics with zap and returns HTTP 500.
func Recovery(log *zap.Logger) gin.HandlerFunc {
	if log == nil {
		log = zap.NewNop()
	}
	logger := log.Named("Recovery")

	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				fields := []zap.Field{
					zap.String("method", c.Request.Method),
					zap.String("path", c.Request.URL.RequestURI()),
					zap.String("ip", c.ClientIP()),
					zap.Any("panic", recovered),
				}
				if route := c.FullPath(); route != "" {
					fields = append(fields, zap.String("route", route))
				}
				if ua := strings.TrimSpace(c.GetHeader("User-Agent")); ua != "" {
					fields = append(fields, zap.String("ua", ua))
				}

				if isBrokenConnection(recovered) {
					logger.Warn("panic recovered on closed connection", fields...)
					c.Abort()
					return
				}

				fields = append(fields, zap.ByteString("stack", debug.Stack()))
				logger.Error("panic recovered", fields...)
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()

		c.Next()
	}
}

func isBrokenConnection(recovered any) bool {
	err, ok := recovered.(error)
	if !ok || err == nil {
		return false
	}

	var netErr *net.OpError
	if errors.As(err, &netErr) {
		err = netErr
	}

	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "broken pipe") || strings.Contains(message, "connection reset by peer")
}
