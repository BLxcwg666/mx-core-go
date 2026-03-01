package middleware

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/pkg/prettylog"
	"go.uber.org/zap"
)

// Logger returns a Gin middleware that logs each request
func Logger(log *zap.Logger) gin.HandlerFunc {
	named := log.Named("LoggingInterceptor")
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		if raw := c.Request.URL.RawQuery; raw != "" {
			path = path + "?" + raw
		}
		content := fmt.Sprintf("%s -> %s", c.Request.Method, path)

		named.Debug(fmt.Sprintf("+++ 收到请求：%s", content))

		c.Next()

		elapsed := time.Since(start).Milliseconds()
		named.Debug(fmt.Sprintf("--- 响应请求：%s%s",
			content,
			prettylog.Yellow(fmt.Sprintf(" +%dms", elapsed)),
		))
	}
}
