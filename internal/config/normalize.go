package config

import (
	"net/http"
	"strings"
)

func normalizeDatabaseConfig(cfg DatabaseRuntimeConfig) DatabaseRuntimeConfig {
	cfg.DSN = strings.TrimSpace(cfg.DSN)
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.User = strings.TrimSpace(cfg.User)
	cfg.Username = strings.TrimSpace(cfg.Username)
	cfg.Password = strings.TrimSpace(cfg.Password)
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.DBName = strings.TrimSpace(cfg.DBName)
	cfg.Charset = strings.TrimSpace(cfg.Charset)
	cfg.Loc = strings.TrimSpace(cfg.Loc)

	if cfg.User == "" && cfg.Username != "" {
		cfg.User = cfg.Username
	}
	if cfg.Name == "" && cfg.DBName != "" {
		cfg.Name = cfg.DBName
	}
	if cfg.Host == "" {
		cfg.Host = defaultDBHost
	}
	if cfg.Port == 0 {
		cfg.Port = defaultDBPort
	}
	if cfg.User == "" {
		cfg.User = defaultDBUser
	}
	if cfg.Password == "" {
		cfg.Password = defaultDBPassword
	}
	if cfg.Name == "" {
		cfg.Name = defaultDBName
	}
	if cfg.Charset == "" {
		cfg.Charset = defaultDBCharset
	}
	if cfg.Loc == "" {
		cfg.Loc = defaultDBLoc
	}
	if cfg.Params != nil {
		cfg.Params = copyStringMap(cfg.Params)
	}
	return cfg
}

func normalizeRedisConfig(cfg RedisRuntimeConfig) RedisRuntimeConfig {
	cfg.URL = normalizeRedisRawURL(cfg.URL)
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Username = strings.TrimSpace(cfg.Username)
	cfg.Password = strings.TrimSpace(cfg.Password)
	cfg.Scheme = strings.ToLower(strings.TrimSpace(cfg.Scheme))

	if cfg.Host == "" && cfg.URL == "" {
		cfg.Host = defaultRedisHost
	}
	if cfg.Port == 0 {
		cfg.Port = defaultRedisPort
	}
	if cfg.DB < 0 {
		cfg.DB = defaultRedisDB
	}
	if cfg.Scheme == "" {
		if cfg.TLS {
			cfg.Scheme = "rediss"
		} else {
			cfg.Scheme = "redis"
		}
	}
	if cfg.Params != nil {
		cfg.Params = copyStringMap(cfg.Params)
	}
	return cfg
}

func normalizeTrustedProxyConfig(cfg TrustedProxyRuntimeConfig) TrustedProxyRuntimeConfig {
	cfg.Headers = normalizeHeaderNames(cfg.Headers)
	if len(cfg.Headers) == 0 {
		cfg.Headers = []string{"CF-Connecting-IP", "X-Forwarded-For", "X-Real-IP"}
	}
	cfg.Proxies = normalizeStringList(cfg.Proxies)
	return cfg
}

func normalizeRedisRawURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "redis://") || strings.HasPrefix(trimmed, "rediss://") {
		return trimmed
	}
	return "redis://" + trimmed
}

func normalizeMeiliConfig(cfg MeiliSearchRuntimeConfig) MeiliSearchRuntimeConfig {
	cfg.URL = strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.IndexName = strings.TrimSpace(cfg.IndexName)

	if cfg.Host == "" && cfg.URL == "" {
		cfg.Host = defaultMeiliHost
	}
	if cfg.Port == 0 {
		cfg.Port = defaultMeiliPort
	}
	if cfg.IndexName == "" {
		cfg.IndexName = defaultMeiliIndex
	}
	return cfg
}

func normalizeOrigins(origins []string) []string {
	return normalizeStringList(origins)
}

func normalizeEnv(env string) string {
	trimmed := strings.ToLower(strings.TrimSpace(env))
	if trimmed == "" {
		return defaultEnv
	}
	return trimmed
}

func normalizeAdminAssetPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultMXAdminPath
	}
	return trimmed
}

func normalizeRuntimePaths(paths RuntimePathsConfig) RuntimePathsConfig {
	paths.Logs = strings.TrimSpace(paths.Logs)
	paths.Backups = strings.TrimSpace(paths.Backups)
	paths.Static = strings.TrimSpace(paths.Static)
	return paths
}

func copyStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		k := strings.TrimSpace(key)
		v := strings.TrimSpace(value)
		if k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

func normalizeStringList(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	out := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, item := range input {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeHeaderNames(headers []string) []string {
	if len(headers) == 0 {
		return nil
	}
	out := make([]string, 0, len(headers))
	seen := make(map[string]struct{}, len(headers))
	for _, header := range headers {
		canonical := normalizeHeaderName(strings.TrimSpace(header))
		if canonical == "" {
			continue
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeHeaderName(header string) string {
	switch strings.ToLower(strings.TrimSpace(header)) {
	case "":
		return ""
	case "cf-connecting-ip":
		return "CF-Connecting-IP"
	case "x-forwarded-for":
		return "X-Forwarded-For"
	case "x-real-ip":
		return "X-Real-IP"
	case "true-client-ip":
		return "True-Client-IP"
	case "fly-client-ip":
		return "Fly-Client-IP"
	default:
		return http.CanonicalHeaderKey(header)
	}
}
