package authn

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	gowauthn "github.com/go-webauthn/webauthn/webauthn"
	pkgredis "github.com/mx-space/core/internal/pkg/redis"
	redis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	authnSessionRedisKeyPrefix = "mx:authn:session:"
	authnSessionStoreTimeout   = 2 * time.Second
	authnSessionFallbackTTL    = time.Minute
)

type authnSessionStore struct {
}

func (s *authnSessionStore) set(key string, value *gowauthn.SessionData) {
	key = strings.TrimSpace(key)
	if key == "" || value == nil {
		return
	}

	if rdb := authnSessionRedis(); rdb != nil {
		payload, err := json.Marshal(value)
		if err == nil {
			ctx, cancel := authnSessionContext()
			defer cancel()
			err = rdb.Set(ctx, authnSessionRedisKey(key), payload, authnSessionTTL(value)).Err()
		}
		if err != nil {
			authnSessionLogger().Warn("persist passkey challenge failed", zap.String("key", key), zap.Error(err))
		}
		return
	}

	authnSessionLogger().Warn("persist passkey challenge skipped: redis unavailable", zap.String("key", key))
}

func (s *authnSessionStore) get(key string) (*gowauthn.SessionData, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, false
	}

	if rdb := authnSessionRedis(); rdb != nil {
		ctx, cancel := authnSessionContext()
		defer cancel()

		data, err := rdb.Get(ctx, authnSessionRedisKey(key)).Bytes()
		switch {
		case err == nil:
			var value gowauthn.SessionData
			if unmarshalErr := json.Unmarshal(data, &value); unmarshalErr != nil {
				authnSessionLogger().Warn("decode passkey challenge failed", zap.String("key", key), zap.Error(unmarshalErr))
				s.del(key)
				return nil, false
			}
			if authnSessionExpired(&value) {
				s.del(key)
				return nil, false
			}
			return &value, true
		case err == redis.Nil:
			return nil, false
		default:
			authnSessionLogger().Warn("read passkey challenge failed", zap.String("key", key), zap.Error(err))
			return nil, false
		}
	}

	authnSessionLogger().Warn("read passkey challenge skipped: redis unavailable", zap.String("key", key))
	return nil, false
}

func (s *authnSessionStore) del(key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}

	if rdb := authnSessionRedis(); rdb != nil {
		ctx, cancel := authnSessionContext()
		defer cancel()
		if err := rdb.Del(ctx, authnSessionRedisKey(key)).Err(); err != nil {
			authnSessionLogger().Warn("delete passkey challenge failed", zap.String("key", key), zap.Error(err))
		}
		return
	}

	authnSessionLogger().Warn("delete passkey challenge skipped: redis unavailable", zap.String("key", key))
}

func authnSessionRedis() *redis.Client {
	if pkgredis.Default == nil {
		return nil
	}
	return pkgredis.Default.Raw()
}

func authnSessionRedisKey(key string) string {
	return authnSessionRedisKeyPrefix + key
}

func authnSessionContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), authnSessionStoreTimeout)
}

func authnSessionTTL(value *gowauthn.SessionData) time.Duration {
	if value == nil || value.Expires.IsZero() {
		return authnSessionFallbackTTL
	}
	ttl := time.Until(value.Expires)
	if ttl <= 0 {
		return time.Second
	}
	return ttl
}

func authnSessionExpired(value *gowauthn.SessionData) bool {
	if value == nil || value.Expires.IsZero() {
		return false
	}
	return value.Expires.Before(time.Now())
}

func authnSessionLogger() *zap.Logger {
	return zap.L().Named("AuthnSessionStore")
}

var authnSessions = &authnSessionStore{}
