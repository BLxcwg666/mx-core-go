package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/pkg/bark"
	"github.com/redis/go-redis/v9"
)

const (
	rateLimitMax    = 50
	rateLimitWindow = time.Second
)

// RateLimit returns a middleware that enforces a sliding-window rate limit of 50
func RateLimit(rdb *redis.Client, barkSvc *bark.Service) gin.HandlerFunc {
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
			c.Next()
			return
		}

		if count == 1 {
			rdb.PExpire(ctx, key, rateLimitWindow+time.Second)
		}

		if count > rateLimitMax {
			path := c.Request.URL.Path
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
