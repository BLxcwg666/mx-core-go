package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/pkg/bark"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	rateLimitMax    = 50
	rateLimitWindow = time.Second
)

// RateLimit returns a middleware that enforces a sliding-window rate limit of 50
func RateLimit(rdb *redis.Client, barkSvc *bark.Service) gin.HandlerFunc {
	logger := zap.L().Named("RateLimit")
	return func(c *gin.Context) {
		if IsAuthenticated(c) {
			c.Next()
			return
		}

		ip := c.ClientIP()
		if ip == "" {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		windowKey := time.Now().Unix()
		key := fmt.Sprintf("mx:rate_limit:%s:%d", ip, windowKey)

		count, err := rdb.Incr(ctx, key).Result()
		if err != nil {
			logger.Warn("redis incr failed, skipping rate limit",
				zap.String("ip", ip),
				zap.String("path", c.Request.URL.Path),
				zap.Error(err),
			)
			c.Next()
			return
		}

		if count == 1 {
			if err := rdb.PExpire(ctx, key, rateLimitWindow+time.Second).Err(); err != nil {
				logger.Warn("redis expire failed for rate limit key",
					zap.String("ip", ip),
					zap.String("path", c.Request.URL.Path),
					zap.String("key", key),
					zap.Error(err),
				)
			}
		}

		if count > rateLimitMax {
			path := c.Request.URL.Path
			logger.Warn("rate limited request",
				zap.String("ip", ip),
				zap.String("path", path),
				zap.Int64("count", count),
			)
			if barkSvc != nil {
				go barkSvc.ThrottlePush(ip, path)
			}
			c.Header("Retry-After", "1")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"ok":      0,
				"code":    http.StatusTooManyRequests,
				"message": "等..等一下，太快了 ∑(っ °Д °;)っ",
			})
			return
		}

		c.Next()
	}
}
