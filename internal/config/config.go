package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func Load(configPath string) (*AppConfig, error) {
	path := strings.TrimSpace(configPath)
	if path == "" {
		path = DefaultConfigPath
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %q: %w", path, err)
	}

	cfg := defaultAppConfig()
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	raw := rawAppConfig{}
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse config file %q: %w", path, err)
	}

	applyRawAppConfig(&cfg, raw)
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("invalid port %d in %q, expected 1-65535", cfg.Port, path)
	}
	if cfg.Database.Port < 1 || cfg.Database.Port > 65535 {
		return nil, fmt.Errorf("invalid database.port %d in %q, expected 1-65535", cfg.Database.Port, path)
	}
	if cfg.Redis.Port < 1 || cfg.Redis.Port > 65535 {
		return nil, fmt.Errorf("invalid redis.port %d in %q, expected 1-65535", cfg.Redis.Port, path)
	}
	if cfg.Redis.DB < 0 {
		return nil, fmt.Errorf("invalid redis.db %d in %q, expected >= 0", cfg.Redis.DB, path)
	}
	if cfg.ClusterWorkers < 0 {
		return nil, fmt.Errorf("invalid cluster_workers %d in %q, expected >= 0", cfg.ClusterWorkers, path)
	}
	if cfg.MeiliSearch.Port < 1 || cfg.MeiliSearch.Port > 65535 {
		return nil, fmt.Errorf("invalid meilisearch.port %d in %q, expected 1-65535", cfg.MeiliSearch.Port, path)
	}

	return &cfg, nil
}

func defaultAppConfig() AppConfig {
	cfg := AppConfig{
		Port:    defaultPort,
		Env:     defaultEnv,
		MXAdmin: defaultMXAdminPath,
		Database: DatabaseRuntimeConfig{
			Host:      defaultDBHost,
			Port:      defaultDBPort,
			User:      defaultDBUser,
			Password:  defaultDBPassword,
			Name:      defaultDBName,
			Charset:   defaultDBCharset,
			ParseTime: true,
			Loc:       defaultDBLoc,
		},
		Redis: RedisRuntimeConfig{
			Host: defaultRedisHost,
			Port: defaultRedisPort,
			DB:   defaultRedisDB,
		},
		MeiliSearch: MeiliSearchRuntimeConfig{
			Host:      defaultMeiliHost,
			Port:      defaultMeiliPort,
			IndexName: defaultMeiliIndex,
		},
	}
	cfg.Database = normalizeDatabaseConfig(cfg.Database)
	cfg.Redis = normalizeRedisConfig(cfg.Redis)
	cfg.DSN = cfg.Database.DSNValue()
	cfg.RedisURL = cfg.Redis.URLValue()
	return cfg
}

func applyRawAppConfig(cfg *AppConfig, raw rawAppConfig) {
	if raw.Port != 0 {
		cfg.Port = raw.Port
	}
	cfg.Database = applyRawDatabaseConfig(cfg.Database, raw)
	cfg.Redis = applyRawRedisConfig(cfg.Redis, raw)
	if v := strings.TrimSpace(raw.Env); v != "" {
		cfg.Env = v
	}
	if v := strings.TrimSpace(raw.NodeEnv); v != "" {
		cfg.Env = v
	}
	if raw.Cluster != nil {
		cfg.Cluster = *raw.Cluster
	}
	if raw.ClusterWorkers != 0 {
		cfg.ClusterWorkers = raw.ClusterWorkers
	}
	if v := strings.TrimSpace(raw.MXAdmin); v != "" {
		cfg.MXAdmin = v
	}
	if v := strings.TrimSpace(raw.MXAdminSnake); v != "" {
		cfg.MXAdmin = v
	}
	if v := strings.TrimSpace(raw.Paths.Logs); v != "" {
		cfg.Paths.Logs = v
	}
	if v := strings.TrimSpace(raw.LogDir); v != "" {
		cfg.Paths.Logs = v
	}
	if v := strings.TrimSpace(raw.LogsDir); v != "" {
		cfg.Paths.Logs = v
	}
	if v := strings.TrimSpace(raw.Paths.Backups); v != "" {
		cfg.Paths.Backups = v
	}
	if v := strings.TrimSpace(raw.BackupDir); v != "" {
		cfg.Paths.Backups = v
	}
	if v := strings.TrimSpace(raw.BackupsDir); v != "" {
		cfg.Paths.Backups = v
	}
	if v := strings.TrimSpace(raw.Paths.Static); v != "" {
		cfg.Paths.Static = v
	}
	if v := strings.TrimSpace(raw.StaticDir); v != "" {
		cfg.Paths.Static = v
	}
	if v := strings.TrimSpace(raw.StaticsDir); v != "" {
		cfg.Paths.Static = v
	}
	if raw.LogRotateSize != nil {
		v := *raw.LogRotateSize
		cfg.LogRotateSize = &v
	}
	if raw.LogRotateKeep != nil {
		v := *raw.LogRotateKeep
		cfg.LogRotateKeep = &v
	}

	switch {
	case raw.AllowedOrigins != nil:
		cfg.AllowedOrigins = normalizeOrigins(raw.AllowedOrigins)
	case raw.CORSAllowedOrigins != nil:
		cfg.AllowedOrigins = normalizeOrigins(raw.CORSAllowedOrigins)
	}

	if v := strings.TrimSpace(raw.JWTSecret); v != "" {
		cfg.JWTSecret = v
	}
	if v := strings.TrimSpace(raw.JWTSecretLegacy); v != "" {
		cfg.JWTSecret = v
	}
	if v := strings.TrimSpace(raw.Timezone); v != "" {
		cfg.Timezone = v
	}
	if v := strings.TrimSpace(raw.TimeZone); v != "" {
		cfg.Timezone = v
	}
	if v := strings.TrimSpace(raw.TZ); v != "" {
		cfg.Timezone = v
	}

	meili := cfg.MeiliSearch
	if raw.MeiliSearch.Enable != nil {
		meili.Enable = *raw.MeiliSearch.Enable
		meili.HasEnable = true
	}
	if v := strings.TrimSpace(raw.MeiliSearch.URL); v != "" {
		meili.URL = v
	}
	if v := strings.TrimSpace(raw.MeiliSearch.Host); v != "" {
		meili.Host = v
	}
	if raw.MeiliSearch.Port != 0 {
		meili.Port = raw.MeiliSearch.Port
	}
	if v := strings.TrimSpace(raw.MeiliSearch.APIKey); v != "" {
		meili.APIKey = v
	}
	if v := strings.TrimSpace(raw.MeiliSearch.MasterKey); v != "" {
		meili.APIKey = v
	}
	if v := strings.TrimSpace(raw.MeiliSearch.IndexName); v != "" {
		meili.IndexName = v
	}
	if raw.MeiliEnable != nil {
		meili.Enable = *raw.MeiliEnable
		meili.HasEnable = true
	}
	if v := strings.TrimSpace(raw.MeiliURL); v != "" {
		meili.URL = v
	}
	if v := strings.TrimSpace(raw.MeiliHost); v != "" {
		meili.Host = v
	}
	if raw.MeiliPort != 0 {
		meili.Port = raw.MeiliPort
	}
	if v := strings.TrimSpace(raw.MeiliAPIKey); v != "" {
		meili.APIKey = v
	}
	if v := strings.TrimSpace(raw.MeiliMasterKey); v != "" {
		meili.APIKey = v
	}
	if v := strings.TrimSpace(raw.MeiliIndexName); v != "" {
		meili.IndexName = v
	}
	cfg.MeiliSearch = normalizeMeiliConfig(meili)
	cfg.DSN = cfg.Database.DSNValue()
	cfg.RedisURL = cfg.Redis.URLValue()
	cfg.MXAdmin = normalizeAdminAssetPath(cfg.MXAdmin)
	cfg.Paths = normalizeRuntimePaths(cfg.Paths)

	cfg.Env = normalizeEnv(cfg.Env)
}

func applyRawDatabaseConfig(current DatabaseRuntimeConfig, raw rawAppConfig) DatabaseRuntimeConfig {
	cfg := current

	if v := strings.TrimSpace(raw.Database.DSN); v != "" {
		cfg.DSN = v
	}
	if v := strings.TrimSpace(raw.Database.URL); v != "" {
		cfg.DSN = v
	}
	if v := strings.TrimSpace(raw.DSN); v != "" {
		cfg.DSN = v
	}
	if v := strings.TrimSpace(raw.DatabaseURL); v != "" {
		cfg.DSN = v
	}
	if v := strings.TrimSpace(raw.Database.Host); v != "" {
		cfg.Host = v
	}
	if v := strings.TrimSpace(raw.DBHost); v != "" {
		cfg.Host = v
	}
	if raw.Database.Port != 0 {
		cfg.Port = raw.Database.Port
	}
	if raw.DBPort != 0 {
		cfg.Port = raw.DBPort
	}
	if v := strings.TrimSpace(raw.Database.User); v != "" {
		cfg.User = v
	}
	if v := strings.TrimSpace(raw.Database.Username); v != "" {
		cfg.User = v
	}
	if v := strings.TrimSpace(raw.DBUser); v != "" {
		cfg.User = v
	}
	if v := strings.TrimSpace(raw.Database.Password); v != "" {
		cfg.Password = v
	}
	if v := strings.TrimSpace(raw.DBPassword); v != "" {
		cfg.Password = v
	}
	if v := strings.TrimSpace(raw.Database.Name); v != "" {
		cfg.Name = v
	}
	if v := strings.TrimSpace(raw.Database.DBName); v != "" {
		cfg.Name = v
	}
	if v := strings.TrimSpace(raw.DBName); v != "" {
		cfg.Name = v
	}
	if v := strings.TrimSpace(raw.Database.Charset); v != "" {
		cfg.Charset = v
	}
	if v := strings.TrimSpace(raw.DBCharset); v != "" {
		cfg.Charset = v
	}
	if raw.Database.ParseTime != nil {
		cfg.ParseTime = *raw.Database.ParseTime
	}
	if raw.DBParseTime != nil {
		cfg.ParseTime = *raw.DBParseTime
	}
	if v := strings.TrimSpace(raw.Database.Loc); v != "" {
		cfg.Loc = v
	}
	if v := strings.TrimSpace(raw.DBLoc); v != "" {
		cfg.Loc = v
	}
	if raw.Database.Params != nil {
		cfg.Params = copyStringMap(raw.Database.Params)
	}

	return normalizeDatabaseConfig(cfg)
}

func applyRawRedisConfig(current RedisRuntimeConfig, raw rawAppConfig) RedisRuntimeConfig {
	cfg := current

	if v := strings.TrimSpace(raw.Redis.URL); v != "" {
		cfg.URL = v
	}
	if v := strings.TrimSpace(raw.RedisURL); v != "" {
		cfg.URL = v
	}
	if v := strings.TrimSpace(raw.Redis.Host); v != "" {
		cfg.Host = v
	}
	if v := strings.TrimSpace(raw.RedisHost); v != "" {
		cfg.Host = v
	}
	if raw.Redis.Port != 0 {
		cfg.Port = raw.Redis.Port
	}
	if raw.RedisPort != 0 {
		cfg.Port = raw.RedisPort
	}
	if v := strings.TrimSpace(raw.Redis.Username); v != "" {
		cfg.Username = v
	}
	if v := strings.TrimSpace(raw.RedisUsername); v != "" {
		cfg.Username = v
	}
	if v := strings.TrimSpace(raw.Redis.Password); v != "" {
		cfg.Password = v
	}
	if v := strings.TrimSpace(raw.RedisPassword); v != "" {
		cfg.Password = v
	}
	if raw.Redis.DB != nil {
		cfg.DB = *raw.Redis.DB
	}
	if raw.RedisDB != nil {
		cfg.DB = *raw.RedisDB
	}
	if raw.Redis.TLS != nil {
		cfg.TLS = *raw.Redis.TLS
	}
	if raw.RedisTLS != nil {
		cfg.TLS = *raw.RedisTLS
	}
	if v := strings.TrimSpace(raw.Redis.Scheme); v != "" {
		cfg.Scheme = v
	}
	if raw.Redis.Params != nil {
		cfg.Params = copyStringMap(raw.Redis.Params)
	}

	return normalizeRedisConfig(cfg)
}

func (c *AppConfig) IsDev() bool {
	return strings.EqualFold(c.Env, defaultEnv)
}

func (c *AppConfig) AdminAssetPath() string {
	return normalizeAdminAssetPath(c.MXAdmin)
}

func (c *AppConfig) LogDir() string {
	if c == nil {
		return ResolveRuntimePath("", "logs")
	}
	return ResolveRuntimePath(c.Paths.Logs, "logs")
}

func (c *AppConfig) LogRotateSizeMB() (int, bool) {
	if c == nil || c.LogRotateSize == nil {
		return 0, false
	}
	return *c.LogRotateSize, true
}

func (c *AppConfig) LogRotateKeepCount() (int, bool) {
	if c == nil || c.LogRotateKeep == nil {
		return 0, false
	}
	return *c.LogRotateKeep, true
}

func (c *AppConfig) BackupDir() string {
	if c == nil {
		return ResolveRuntimePath("", "backups")
	}
	return ResolveRuntimePath(c.Paths.Backups, "backups")
}

func (c *AppConfig) StaticDir() string {
	if c == nil {
		return ResolveRuntimePath("", "static")
	}
	return ResolveRuntimePath(c.Paths.Static, "static")
}

// DefaultFullConfig returns sensible defaults matching the original TypeScript defaults.
func DefaultFullConfig() FullConfig {
	return FullConfig{
		SEO: SEOConfig{
			Title:       "我的小世界呀",
			Description: "哈喽~欢迎光临",
			Keywords:    []string{},
		},
		URL: URLConfig{
			WSURL:     "http://localhost:2333",
			AdminURL:  "http://localhost:2333/proxy/qaqdmin",
			ServerURL: "http://localhost:2333",
			WebURL:    "http://localhost:2323",
		},
		MailOptions: MailOptions{
			Enable:   false,
			Provider: "smtp",
			From:     "",
			SMTP: &SMTPConfig{
				User: "",
				Pass: "",
				Options: SMTPOptions{
					Host:   "",
					Port:   465,
					Secure: true,
				},
			},
			Resend: &ResendConfig{
				APIKey: "",
			},
		},
		CommentOptions: CommentOptions{
			AntiSpam:           false,
			AIReview:           false,
			RecordIPLocation:   true,
			AIReviewType:       "binary",
			AIReviewThreshold:  5,
			TestAIReview:       "__action__",
			DisableComment:     false,
			BlockIPs:           []string{},
			DisableNoChinese:   false,
			SpamKeywords:       []string{},
			CommentShouldAudit: false,
		},
		BarkOptions: BarkOptions{
			Enable:              false,
			Key:                 "",
			ServerURL:           "https://api.day.app",
			EnableComment:       true,
			EnableThrottleGuard: false,
		},
		FriendLinkOptions: FriendLinkOptions{
			AllowApply:                  true,
			AllowSubPath:                false,
			EnableAvatarInternalization: true,
		},
		S3Options: S3Options{
			Endpoint:        "",
			AccessKeyID:     "",
			SecretAccessKey: "",
			Bucket:          "",
			Region:          "",
			CustomDomain:    "",
			PathStyleAccess: false,
		},
		BackupOptions: BackupOptions{
			Enable: false,
			Path:   "backups/{Y}/{m}/backup-{Y}{m}{d}-{h}{i}{s}.zip",
		},
		ImageBedOptions: ImageBedOptions{
			Enable:         false,
			Path:           "images/{Y}/{m}/{uuid}.{ext}",
			AllowedFormats: "jpg,jpeg,png,gif,webp",
			MaxSizeMB:      10,
		},
		ImageStorageOptions: ImageStorageOptions{
			Enable:               false,
			SyncOnPublish:        false,
			DeleteLocalAfterSync: false,
			Endpoint:             nil,
			SecretID:             nil,
			SecretKey:            nil,
			Bucket:               nil,
			Region:               "auto",
			CustomDomain:         "",
			Prefix:               "",
		},
		BaiduSearchOptions: BaiduSearchOptions{
			Enable: false,
			Token:  nil,
		},
		BingSearchOptions: BingSearchOptions{
			Enable: false,
			Token:  nil,
		},
		AlgoliaSearchOptions: AlgoliaSearchOptions{
			Enable:          false,
			APIKey:          "",
			AppID:           "",
			IndexName:       "",
			MaxTruncateSize: 10000,
		},
		AdminExtra: AdminExtra{
			EnableAdminProxy: true,
			Background:       "",
			GaodeMapKey:      nil,
		},
		TextOptions: TextOptions{
			Macros: true,
		},
		FeatureList: FeatureList{
			EmailSubscribe: false,
		},
		ThirdPartyServiceIntegration: ThirdPartyServiceIntegration{
			GitHubToken: "",
		},
		AuthSecurity: AuthSecurity{
			DisablePasswordLogin: false,
		},
		MeiliSearchOptions: MeiliSearchOptions{
			Enable:         true,
			IndexName:      "mx-space",
			SearchCacheTTL: 300,
		},
		AI: AIConfig{
			Providers:                 []AIProvider{},
			EnableSummary:             false,
			EnableAutoGenerateSummary: false,
			AISummaryTargetLanguage:   "auto",
		},
		OAuth: OAuthConfig{
			Providers: []OAuthProvider{},
			Secrets:   map[string]interface{}{},
			Public:    map[string]interface{}{},
		},
	}
}
