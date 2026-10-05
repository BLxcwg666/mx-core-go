package search

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"time"

	pkgredis "github.com/mx-space/core/internal/pkg/redis"
)

const (
	searchCachePrefix     = "mx:search:meili:"
	searchCacheVersionKey = "mx:search:meili:version"
	searchCacheTimeout    = time.Second
)

// Raw MeiliSearch hits are cached for meiliSearchOptions.searchCacheTTL seconds. Visibility filtering and
// hydration still run per request, and any index change bumps the version so stale hits are not served.

func (s *Service) searchCacheTTL() time.Duration {
	cfg, err := s.cfgSvc.Get()
	if err != nil || cfg == nil || cfg.MeiliSearchOptions.SearchCacheTTL <= 0 {
		return 0
	}
	return time.Duration(cfg.MeiliSearchOptions.SearchCacheTTL) * time.Second
}

func searchCacheKey(ctx context.Context, rc *pkgredis.Client, q string) string {
	version, _ := rc.Get(ctx, searchCacheVersionKey)
	sum := sha1.Sum([]byte(q))
	return searchCachePrefix + version + ":" + hex.EncodeToString(sum[:])
}

func (s *Service) cachedMeiliSearch(client *meiliClient, q string) ([]SearchResult, error) {
	ttl := s.searchCacheTTL()
	rc := pkgredis.Default
	if ttl <= 0 || rc == nil {
		return client.Search(q)
	}
	ctx, cancel := context.WithTimeout(context.Background(), searchCacheTimeout)
	defer cancel()
	key := searchCacheKey(ctx, rc, q)
	if raw, err := rc.Get(ctx, key); err == nil && raw != "" {
		var cached []SearchResult
		if json.Unmarshal([]byte(raw), &cached) == nil {
			return cached, nil
		}
	}
	results, err := client.Search(q)
	if err != nil {
		return nil, err
	}
	if data, err := json.Marshal(results); err == nil {
		_ = rc.Set(ctx, key, string(data), ttl)
	}
	return results, nil
}

func invalidateSearchCache() {
	rc := pkgredis.Default
	if rc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), searchCacheTimeout)
	defer cancel()
	_ = rc.Raw().Incr(ctx, searchCacheVersionKey).Err()
}
