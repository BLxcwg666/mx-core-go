package middleware

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	APICachePrefix            = "mx-api-cache:"
	defaultHTTPCacheTTL       = 15 * time.Second
	defaultHTTPCacheMaxBody   = 1 << 20 // 1 MiB
	staleWhileRevalidateValue = 60
)

type HTTPCacheOptions struct {
	TTL                    time.Duration
	EnableCDNHeader        bool
	EnableForceCacheHeader bool
	Disable                bool
	SkipPaths              []string
	MaxBodyBytes           int
}

type cachedHTTPResponse struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type,omitempty"`
	BodyBase64  string `json:"body_base64"`
	Body        []byte `json:"-"`
}

type cacheBodyWriter struct {
	gin.ResponseWriter
	body         []byte
	maxBodyBytes int
	overflow     bool
}

func (w *cacheBodyWriter) Write(data []byte) (int, error) {
	w.capture(data)
	return w.ResponseWriter.Write(data)
}

func (w *cacheBodyWriter) WriteString(s string) (int, error) {
	w.capture([]byte(s))
	return w.ResponseWriter.WriteString(s)
}

func (w *cacheBodyWriter) capture(data []byte) {
	if w.maxBodyBytes <= 0 || w.overflow || len(data) == 0 {
		return
	}
	remaining := w.maxBodyBytes - len(w.body)
	if remaining <= 0 {
		w.overflow = true
		return
	}
	if len(data) > remaining {
		w.body = append(w.body, data[:remaining]...)
		w.overflow = true
		return
	}
	w.body = append(w.body, data...)
}

func normalizeHTTPCacheOptions(opts HTTPCacheOptions) HTTPCacheOptions {
	if opts.TTL <= 0 {
		opts.TTL = defaultHTTPCacheTTL
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = defaultHTTPCacheMaxBody
	}
	return opts
}

func HTTPCache(rdb *redis.Client, opts HTTPCacheOptions) gin.HandlerFunc {
	options := normalizeHTTPCacheOptions(opts)
	return func(c *gin.Context) {
		if options.Disable || rdb == nil || c.Request.Method != http.MethodGet {
			c.Next()
			return
		}

		path := c.Request.URL.Path
		if shouldSkipCachePath(path, options.SkipPaths) || hasBypassTimestamp(c) {
			c.Next()
			return
		}

		if IsAuthenticated(c) {
			c.Next()
			setPrivateCacheHeader(c.Writer, c.Writer.Status())
			return
		}

		cacheKey := APICachePrefix + c.Request.URL.RequestURI()
		if payload, ok := readCachedResponse(c.Request.Context(), rdb, cacheKey); ok {
			setCacheHeader(c.Writer, payload.Status, int(options.TTL/time.Second), options)
			c.Data(payload.Status, payload.ContentType, payload.Body)
			c.Abort()
			return
		}

		buffer := &cacheBodyWriter{
			ResponseWriter: c.Writer,
			maxBodyBytes:   options.MaxBodyBytes,
		}
		c.Writer = buffer
		c.Next()

		status := c.Writer.Status()
		if status <= 0 {
			status = http.StatusOK
		}

		if !isCacheableResponse(status, c.Writer.Header()) {
			return
		}

		setCacheHeader(c.Writer, status, int(options.TTL/time.Second), options)
		if buffer.overflow || len(buffer.body) == 0 {
			return
		}

		payload := cachedHTTPResponse{
			Status:      status,
			ContentType: c.Writer.Header().Get("Content-Type"),
			BodyBase64:  base64.StdEncoding.EncodeToString(buffer.body),
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return
		}
		_ = rdb.Set(c.Request.Context(), cacheKey, raw, options.TTL).Err()
	}
}

func PurgeHTTPCache(ctx context.Context, rdb *redis.Client) (int64, error) {
	if rdb == nil {
		return 0, nil
	}
	var (
		cursor  uint64
		deleted int64
	)
	for {
		keys, next, err := rdb.Scan(ctx, cursor, APICachePrefix+"*", 200).Result()
		if err != nil {
			return deleted, err
		}
		if len(keys) > 0 {
			n, err := rdb.Del(ctx, keys...).Result()
			if err != nil {
				return deleted, err
			}
			deleted += n
		}
		cursor = next
		if cursor == 0 {
			return deleted, nil
		}
	}
}

func readCachedResponse(ctx context.Context, rdb *redis.Client, cacheKey string) (cachedHTTPResponse, bool) {
	raw, err := rdb.Get(ctx, cacheKey).Bytes()
	if err != nil || len(raw) == 0 {
		return cachedHTTPResponse{}, false
	}
	var payload cachedHTTPResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return cachedHTTPResponse{}, false
	}
	if payload.Status <= 0 {
		payload.Status = http.StatusOK
	}
	if payload.ContentType == "" {
		payload.ContentType = "application/json; charset=utf-8"
	}
	body, err := base64.StdEncoding.DecodeString(payload.BodyBase64)
	if err != nil {
		return cachedHTTPResponse{}, false
	}
	payload.Body = body
	return payload, true
}

func shouldSkipCachePath(path string, patterns []string) bool {
	for _, pattern := range patterns {
		p := strings.TrimSpace(pattern)
		if p == "" {
			continue
		}
		if strings.HasSuffix(p, "*") {
			if strings.HasPrefix(path, strings.TrimSuffix(p, "*")) {
				return true
			}
			continue
		}
		if path == p {
			return true
		}
	}
	return false
}

func hasBypassTimestamp(c *gin.Context) bool {
	query := c.Request.URL.Query()
	for _, key := range []string{"ts", "timestamp", "_t", "t"} {
		if strings.TrimSpace(query.Get(key)) != "" {
			return true
		}
	}
	return false
}

func isCacheableResponse(status int, headers http.Header) bool {
	if status != http.StatusOK {
		return false
	}
	cacheControl := strings.ToLower(headers.Get("Cache-Control"))
	return !strings.Contains(cacheControl, "no-cache") &&
		!strings.Contains(cacheControl, "no-store") &&
		!strings.Contains(cacheControl, "private")
}

func setPrivateCacheHeader(w gin.ResponseWriter, status int) {
	if status != http.StatusOK {
		return
	}
	cacheValue := "private, max-age=0, no-cache, no-store, must-revalidate"
	w.Header().Set("cdn-cache-control", cacheValue)
	w.Header().Set("cache-control", cacheValue)
	w.Header().Set("cloudflare-cdn-cache-control", cacheValue)
}

func setCacheHeader(w gin.ResponseWriter, status, ttlSeconds int, opts HTTPCacheOptions) {
	if status != http.StatusOK {
		return
	}
	if ttlSeconds <= 0 {
		ttlSeconds = int(defaultHTTPCacheTTL / time.Second)
	}
	w.Header().Set("x-mx-cache", "hit")
	if opts.EnableCDNHeader {
		cacheValue := "max-age=" + intToString(ttlSeconds) + ", stale-while-revalidate=" + intToString(staleWhileRevalidateValue)
		w.Header().Set("cdn-cache-control", cacheValue)
		w.Header().Set("Cloudflare-CDN-Cache-Control", cacheValue)
	}
	if w.Header().Get("cache-control") != "" {
		return
	}
	cacheHeaderValue := ""
	if opts.EnableForceCacheHeader {
		cacheHeaderValue = "max-age=" + intToString(ttlSeconds)
	}
	if opts.EnableCDNHeader {
		if cacheHeaderValue != "" {
			cacheHeaderValue += ", "
		}
		cacheHeaderValue += "s-maxage=" + intToString(ttlSeconds) + ", stale-while-revalidate=" + intToString(staleWhileRevalidateValue)
	}
	if cacheHeaderValue != "" {
		w.Header().Set("cache-control", cacheHeaderValue)
	}
}

func intToString(v int) string {
	if v <= 0 {
		return "0"
	}
	return strconv.Itoa(v)
}
