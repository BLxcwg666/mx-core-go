package middleware

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/pkg/bark"
	"github.com/mx-space/core/internal/pkg/response"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	rateLimitBurstWindow   = 2 * time.Second
	rateLimitBurstMax      = 90
	rateLimitShortWindow   = 30 * time.Second
	rateLimitShortMax      = 700
	rateLimitLongWindow    = 5 * time.Minute
	rateLimitLongMax       = 3000
	rateLimitConcurrentMax = 24
	rateLimitInFlightTTL   = 2 * time.Minute
	rateLimitStrikeTTL     = 20 * time.Minute
	rateLimitPenaltyBase   = 15 * time.Second
	rateLimitPenaltyMax    = 10 * time.Minute
)

type rateLimitSnapshot struct {
	Burst    int64
	Short    int64
	Long     int64
	InFlight int64
}

// RateLimit applies a multi-layer HTTP shield with burst, sustained, cooldown,
// and in-flight request guards.
func RateLimit(rdb *redis.Client, barkSvc *bark.Service) gin.HandlerFunc {
	logger := zap.L().Named("RateLimit")
	return func(c *gin.Context) {
		if rdb == nil {
			c.Next()
			return
		}

		ip := c.ClientIP()
		if ip == "" {
			c.Next()
			return
		}
		if isLoopbackIP(ip) {
			c.Next()
			return
		}

		path := c.Request.URL.Path
		ctx := c.Request.Context()

		penaltyTTL, blocked, err := readRateLimitPenalty(ctx, rdb, ip)
		if err != nil {
			logger.Warn("redis penalty check failed, skipping rate limit",
				zap.String("ip", ip),
				zap.String("path", path),
				zap.Error(err),
			)
			c.Next()
			return
		}
		if blocked {
			rejectRateLimited(c, penaltyTTL, "你被丢小黑屋了，等会再试吧 (((ﾟДﾟ;)))")
			return
		}

		cost, class := rateLimitRequestCost(c)
		if cost <= 0 {
			c.Next()
			return
		}

		trackInFlight := shouldTrackInFlight(c)
		snapshot, err := reserveRateLimit(ctx, rdb, ip, cost, trackInFlight)
		if err != nil {
			logger.Warn("redis reserve failed, skipping rate limit",
				zap.String("ip", ip),
				zap.String("path", path),
				zap.String("class", class),
				zap.Int64("cost", cost),
				zap.Error(err),
			)
			c.Next()
			return
		}

		if trackInFlight {
			defer releaseRateLimitInFlight(ctx, rdb, ip, logger)
		}

		if trackInFlight && snapshot.InFlight > rateLimitConcurrentMax {
			penaltyTTL, penaltyErr := registerRateLimitPenalty(ctx, rdb, ip)
			if penaltyErr != nil {
				logger.Warn("failed to register concurrency penalty",
					zap.String("ip", ip),
					zap.String("path", path),
					zap.Error(penaltyErr),
				)
				penaltyTTL = rateLimitPenaltyBase
			}
			logger.Warn("http shield blocked concurrent request burst",
				zap.String("ip", ip),
				zap.String("path", path),
				zap.Int64("cost", cost),
				zap.String("class", class),
				zap.Int64("inflight", snapshot.InFlight),
				zap.Duration("penalty", penaltyTTL),
			)
			if barkSvc != nil {
				go barkSvc.ThrottlePush(ip, path)
			}
			rejectRateLimited(c, penaltyTTL, "太...太快了，等一下 Σ(lliдﾟﾉ)ﾉ")
			return
		}

		if snapshot.Burst > rateLimitBurstMax || snapshot.Short > rateLimitShortMax || snapshot.Long > rateLimitLongMax {
			penaltyTTL, penaltyErr := registerRateLimitPenalty(ctx, rdb, ip)
			if penaltyErr != nil {
				logger.Warn("failed to register rate limit penalty",
					zap.String("ip", ip),
					zap.String("path", path),
					zap.Error(penaltyErr),
				)
				penaltyTTL = rateLimitPenaltyBase
			}
			logger.Warn("http shield blocked request",
				zap.String("ip", ip),
				zap.String("path", path),
				zap.Int64("cost", cost),
				zap.String("class", class),
				zap.Int64("burst", snapshot.Burst),
				zap.Int64("short", snapshot.Short),
				zap.Int64("long", snapshot.Long),
				zap.Duration("penalty", penaltyTTL),
			)
			if barkSvc != nil {
				go barkSvc.ThrottlePush(ip, path)
			}
			rejectRateLimited(c, penaltyTTL, "好...好多人，等一下 Σ(*ﾟдﾟﾉ)ﾉ")
			return
		}

		c.Next()
	}
}

func readRateLimitPenalty(ctx context.Context, rdb *redis.Client, ip string) (time.Duration, bool, error) {
	ttl, err := rdb.TTL(ctx, rateLimitPenaltyKey(ip)).Result()
	if err != nil {
		return 0, false, err
	}
	switch {
	case ttl > 0:
		return ttl, true, nil
	case ttl == -1:
		return rateLimitPenaltyBase, true, nil
	default:
		return 0, false, nil
	}
}

func reserveRateLimit(ctx context.Context, rdb *redis.Client, ip string, cost int64, trackInFlight bool) (rateLimitSnapshot, error) {
	now := time.Now().UTC()
	burstKey := rateLimitBucketKey("burst", ip, now, rateLimitBurstWindow)
	shortKey := rateLimitBucketKey("short", ip, now, rateLimitShortWindow)
	longKey := rateLimitBucketKey("long", ip, now, rateLimitLongWindow)

	pipe := rdb.Pipeline()
	burstCmd := pipe.IncrBy(ctx, burstKey, cost)
	pipe.PExpire(ctx, burstKey, rateLimitBurstWindow+time.Second)
	shortCmd := pipe.IncrBy(ctx, shortKey, cost)
	pipe.PExpire(ctx, shortKey, rateLimitShortWindow+5*time.Second)
	longCmd := pipe.IncrBy(ctx, longKey, cost)
	pipe.PExpire(ctx, longKey, rateLimitLongWindow+time.Minute)

	var inFlightCmd *redis.IntCmd
	if trackInFlight {
		inFlightCmd = pipe.Incr(ctx, rateLimitInFlightKey(ip))
		pipe.PExpire(ctx, rateLimitInFlightKey(ip), rateLimitInFlightTTL)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return rateLimitSnapshot{}, err
	}

	snapshot := rateLimitSnapshot{
		Burst: burstCmd.Val(),
		Short: shortCmd.Val(),
		Long:  longCmd.Val(),
	}
	if inFlightCmd != nil {
		snapshot.InFlight = inFlightCmd.Val()
	}
	return snapshot, nil
}

func releaseRateLimitInFlight(ctx context.Context, rdb *redis.Client, ip string, logger *zap.Logger) {
	key := rateLimitInFlightKey(ip)
	count, err := rdb.Decr(ctx, key).Result()
	if err != nil {
		logger.Warn("failed to release in-flight request slot",
			zap.String("ip", ip),
			zap.String("key", key),
			zap.Error(err),
		)
		return
	}
	if count <= 0 {
		if err := rdb.Del(ctx, key).Err(); err != nil {
			logger.Warn("failed to cleanup in-flight request key",
				zap.String("ip", ip),
				zap.String("key", key),
				zap.Error(err),
			)
		}
		return
	}
	if err := rdb.PExpire(ctx, key, rateLimitInFlightTTL).Err(); err != nil {
		logger.Warn("failed to refresh in-flight request ttl",
			zap.String("ip", ip),
			zap.String("key", key),
			zap.Error(err),
		)
	}
}

func registerRateLimitPenalty(ctx context.Context, rdb *redis.Client, ip string) (time.Duration, error) {
	strikeKey := rateLimitStrikeKey(ip)
	strikes, err := rdb.Incr(ctx, strikeKey).Result()
	if err != nil {
		return 0, err
	}
	_ = rdb.PExpire(ctx, strikeKey, rateLimitStrikeTTL).Err()

	penalty := rateLimitPenaltyDuration(strikes)
	_ = rdb.Set(ctx, rateLimitPenaltyKey(ip), "1", penalty).Err()
	return penalty, nil
}

func rateLimitPenaltyDuration(strikes int64) time.Duration {
	if strikes <= 1 {
		return rateLimitPenaltyBase
	}
	penalty := rateLimitPenaltyBase
	for i := int64(1); i < strikes; i++ {
		if penalty >= rateLimitPenaltyMax {
			return rateLimitPenaltyMax
		}
		penalty *= 2
	}
	if penalty > rateLimitPenaltyMax {
		return rateLimitPenaltyMax
	}
	return penalty
}

func rateLimitRequestCost(c *gin.Context) (int64, string) {
	method := strings.ToUpper(strings.TrimSpace(c.Request.Method))
	path := normalizeRateLimitPath(c.Request.URL.Path)

	switch method {
	case http.MethodOptions:
		return 0, "preflight"
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		if isRateLimitHeavyPath(path) {
			return 4, "heavy-write"
		}
		if isRateLimitAuthPath(path) {
			return 4, "auth"
		}
		if c.Request.ContentLength > 1<<20 {
			return 3, "large-write"
		}
		return 2, "write"
	default:
		cost := int64(1)
		class := "read"
		if isRateLimitHeavyPath(path) {
			cost += 2
			class = "heavy-read"
		}
		if isRateLimitAuthPath(path) {
			cost += 2
			class = "auth"
		}
		if shouldTrackInFlight(c) == false {
			cost++
			class = "upgrade"
		}
		if c.Request.ContentLength > 1<<20 {
			cost++
			class = "large-read"
		}
		return cost, class
	}
}

func isRateLimitHeavyPath(path string) bool {
	if path == "/socket.io" || strings.HasPrefix(path, "/socket.io/") {
		return true
	}
	for _, fragment := range []string{"/proxy/", "/search", "/aggregate", "/analyze", "/render", "/markdown", "/summary", "/ai"} {
		if strings.Contains(path, fragment) {
			return true
		}
	}
	return false
}

func isRateLimitAuthPath(path string) bool {
	for _, fragment := range []string{"/auth", "/login", "/session"} {
		if strings.Contains(path, fragment) {
			return true
		}
	}
	return false
}

func shouldTrackInFlight(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return true
	}
	if upgrade := strings.ToLower(strings.TrimSpace(c.GetHeader("Upgrade"))); upgrade != "" {
		return false
	}
	connection := strings.ToLower(strings.TrimSpace(c.GetHeader("Connection")))
	if strings.Contains(connection, "upgrade") {
		return false
	}
	path := normalizeRateLimitPath(c.Request.URL.Path)
	return path != "/socket.io" && !strings.HasPrefix(path, "/socket.io/")
}

func rejectRateLimited(c *gin.Context, ttl time.Duration, prefix string) {
	retryAfter := retryAfterSeconds(ttl)
	if retryAfter < 1 {
		retryAfter = 1
	}
	response.MarkErrorLogged(c)
	c.Header("Connection", "close")
	c.Header("Retry-After", strconv.Itoa(retryAfter))
	c.Header("X-MX-Shield", "active")
	response.TooManyRequests(c, fmt.Sprintf("%s，请 %d 秒后再试", prefix, retryAfter))
}

func retryAfterSeconds(ttl time.Duration) int {
	if ttl <= 0 {
		return 1
	}
	seconds := int(ttl / time.Second)
	if ttl%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}

func isLoopbackIP(raw string) bool {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil {
		return raw == "localhost"
	}
	return ip.IsLoopback()
}

func normalizeRateLimitPath(path string) string {
	p := strings.ToLower(strings.TrimSpace(path))
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

func rateLimitBucketKey(bucket, ip string, now time.Time, window time.Duration) string {
	return fmt.Sprintf("mx:rate_limit:%s:%s:%d", bucket, ip, now.UnixNano()/int64(window))
}

func rateLimitInFlightKey(ip string) string {
	return fmt.Sprintf("mx:rate_limit:inflight:%s", ip)
}

func rateLimitStrikeKey(ip string) string {
	return fmt.Sprintf("mx:rate_limit:strike:%s", ip)
}

func rateLimitPenaltyKey(ip string) string {
	return fmt.Sprintf("mx:rate_limit:penalty:%s", ip)
}
