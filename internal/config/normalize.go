package config

import "strings"

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
	out := make([]string, 0, len(origins))
	for _, origin := range origins {
		trimmed := strings.TrimSpace(origin)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
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
