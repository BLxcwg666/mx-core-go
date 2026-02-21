package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	idempotenceHeader = "x-idempotence"
	idempotenceTTL    = 60 * time.Second
)

// Idempotence returns a middleware that prevents duplicate non-GET requests from
func Idempotence(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet {
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

	if len(body) == 0 && ua == "" && ip == "" {
		return "", nil
	}

	raw := c.Request.Method + "|" + c.Request.URL.String() + "|" + string(body) + "|" + ua + "|" + ip
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:]), nil
}
