package config

import (
	"bytes"
	"fmt"
	"net"
	neturl "net/url"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultConfigPath is used when --config is not provided.
	DefaultConfigPath = "config.yml"
	defaultPort       = 2333
	// defaultDSN        = "root:password@tcp(127.0.0.1:3306)/mx_space?charset=utf8mb4&parseTime=True&loc=Local"
	// defaultRedisURL   = "redis://localhost:6379/0"
	defaultEnv         = "development"
	defaultMeiliHost   = "localhost"
	defaultMeiliPort   = 7700
	defaultMeiliIndex  = "mx-space"
	defaultDBHost      = "127.0.0.1"
	defaultDBPort      = 3306
	defaultDBUser      = "root"
	defaultDBPassword  = "password"
	defaultDBName      = "mx_space"
	defaultDBCharset   = "utf8mb4"
	defaultDBLoc       = "Local"
	defaultRedisHost   = "localhost"
	defaultRedisPort   = 6379
	defaultRedisDB     = 0
	defaultMXAdminPath = "admin"
)

// AppConfig holds runtime startup configuration loaded from YAML.
type AppConfig struct {
	Port           int                      `yaml:"port"`
	DSN            string                   `yaml:"dsn"` // MySQL DSN
	RedisURL       string                   `yaml:"redis_url"`
	Database       DatabaseRuntimeConfig    `yaml:"database"`
	Redis          RedisRuntimeConfig       `yaml:"redis"`
	Env            string                   `yaml:"env"` // "development" | "production"
	MXAdmin        string                   `yaml:"mx-admin"`
	AllowedOrigins []string                 `yaml:"allowed_origins"`
	JWTSecret      string                   `yaml:"jwt_secret"`
	Timezone       string                   `yaml:"timezone"`
	MeiliSearch    MeiliSearchRuntimeConfig `yaml:"meilisearch"`
}

type DatabaseRuntimeConfig struct {
	DSN       string            `yaml:"dsn"`
	URL       string            `yaml:"url"`
	Host      string            `yaml:"host"`
	Port      int               `yaml:"port"`
	User      string            `yaml:"user"`
	Username  string            `yaml:"username"`
	Password  string            `yaml:"password"`
	Name      string            `yaml:"name"`
	DBName    string            `yaml:"db_name"`
	Charset   string            `yaml:"charset"`
	ParseTime bool              `yaml:"parse_time"`
	Loc       string            `yaml:"loc"`
	Params    map[string]string `yaml:"params"`
}

type RedisRuntimeConfig struct {
	URL      string            `yaml:"url"`
	Host     string            `yaml:"host"`
	Port     int               `yaml:"port"`
	Username string            `yaml:"username"`
	Password string            `yaml:"password"`
	DB       int               `yaml:"db"`
	TLS      bool              `yaml:"tls"`
	Scheme   string            `yaml:"scheme"`
	Params   map[string]string `yaml:"params"`
}

type MeiliSearchRuntimeConfig struct {
	Enable    bool   `yaml:"enable"`
	HasEnable bool   `yaml:"-"`
	URL       string `yaml:"url"`
	Host      string `yaml:"host"`
	Port      int    `yaml:"port"`
	APIKey    string `yaml:"api_key"`
	IndexName string `yaml:"index_name"`
}

type rawAppConfig struct {
	Port               int                  `yaml:"port"`
	DSN                string               `yaml:"dsn"`
	DatabaseURL        string               `yaml:"database_url"`
	RedisURL           string               `yaml:"redis_url"`
	Database           rawDatabaseConfig    `yaml:"database"`
	Redis              rawRedisConfig       `yaml:"redis"`
	DBHost             string               `yaml:"db_host"`
	DBPort             int                  `yaml:"db_port"`
	DBUser             string               `yaml:"db_user"`
	DBPassword         string               `yaml:"db_password"`
	DBName             string               `yaml:"db_name"`
	DBCharset          string               `yaml:"db_charset"`
	DBLoc              string               `yaml:"db_loc"`
	DBParseTime        *bool                `yaml:"db_parse_time"`
	RedisHost          string               `yaml:"redis_host"`
	RedisPort          int                  `yaml:"redis_port"`
	RedisUsername      string               `yaml:"redis_username"`
	RedisPassword      string               `yaml:"redis_password"`
	RedisDB            *int                 `yaml:"redis_db"`
	RedisTLS           *bool                `yaml:"redis_tls"`
	Env                string               `yaml:"env"`
	NodeEnv            string               `yaml:"node_env"`
	MXAdmin            string               `yaml:"mx-admin"`
	MXAdminSnake       string               `yaml:"mx_admin"`
	AllowedOrigins     []string             `yaml:"allowed_origins"`
	CORSAllowedOrigins []string             `yaml:"cors_allowed_origins"`
	JWTSecret          string               `yaml:"jwt_secret"`
	JWTSecretLegacy    string               `yaml:"jwtsecret"`
	Timezone           string               `yaml:"timezone"`
	TimeZone           string               `yaml:"time_zone"`
	TZ                 string               `yaml:"tz"`
	MeiliSearch        rawMeiliSearchConfig `yaml:"meilisearch"`
	MeiliEnable        *bool                `yaml:"meili_enable"`
	MeiliURL           string               `yaml:"meili_url"`
	MeiliHost          string               `yaml:"meili_host"`
	MeiliPort          int                  `yaml:"meili_port"`
	MeiliAPIKey        string               `yaml:"meili_api_key"`
	MeiliMasterKey     string               `yaml:"meili_master_key"`
	MeiliIndexName     string               `yaml:"meili_index_name"`
}

type rawDatabaseConfig struct {
	DSN       string            `yaml:"dsn"`
	URL       string            `yaml:"url"`
	Host      string            `yaml:"host"`
	Port      int               `yaml:"port"`
	User      string            `yaml:"user"`
	Username  string            `yaml:"username"`
	Password  string            `yaml:"password"`
	Name      string            `yaml:"name"`
	DBName    string            `yaml:"db_name"`
	Charset   string            `yaml:"charset"`
	ParseTime *bool             `yaml:"parse_time"`
	Loc       string            `yaml:"loc"`
	Params    map[string]string `yaml:"params"`
}

type rawRedisConfig struct {
	URL      string            `yaml:"url"`
	Host     string            `yaml:"host"`
	Port     int               `yaml:"port"`
	Username string            `yaml:"username"`
	Password string            `yaml:"password"`
	DB       *int              `yaml:"db"`
	TLS      *bool             `yaml:"tls"`
	Scheme   string            `yaml:"scheme"`
	Params   map[string]string `yaml:"params"`
}

type rawMeiliSearchConfig struct {
	Enable    *bool  `yaml:"enable"`
	URL       string `yaml:"url"`
	Host      string `yaml:"host"`
	Port      int    `yaml:"port"`
	APIKey    string `yaml:"api_key"`
	MasterKey string `yaml:"master_key"`
	IndexName string `yaml:"index_name"`
}

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
	if v := strings.TrimSpace(raw.MXAdmin); v != "" {
		cfg.MXAdmin = v
	}
	if v := strings.TrimSpace(raw.MXAdminSnake); v != "" {
		cfg.MXAdmin = v
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

func (c DatabaseRuntimeConfig) DSNValue() string {
	if v := strings.TrimSpace(c.DSN); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.URL); v != "" {
		return v
	}

	host := strings.TrimSpace(c.Host)
	if host == "" {
		host = defaultDBHost
	}
	port := c.Port
	if port == 0 {
		port = defaultDBPort
	}
	user := strings.TrimSpace(c.User)
	if user == "" {
		user = strings.TrimSpace(c.Username)
	}
	if user == "" {
		user = defaultDBUser
	}
	password := strings.TrimSpace(c.Password)
	if password == "" {
		password = defaultDBPassword
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = strings.TrimSpace(c.DBName)
	}
	if name == "" {
		name = defaultDBName
	}
	charset := strings.TrimSpace(c.Charset)
	if charset == "" {
		charset = defaultDBCharset
	}
	loc := strings.TrimSpace(c.Loc)
	if loc == "" {
		loc = defaultDBLoc
	}

	params := neturl.Values{}
	for key, value := range c.Params {
		k := strings.TrimSpace(key)
		v := strings.TrimSpace(value)
		if k != "" && v != "" {
			params.Set(k, v)
		}
	}
	if params.Get("charset") == "" {
		params.Set("charset", charset)
	}
	if params.Get("parseTime") == "" {
		params.Set("parseTime", strconv.FormatBool(c.ParseTime))
	}
	if params.Get("loc") == "" {
		params.Set("loc", loc)
	}

	auth := ""
	if user != "" || password != "" {
		auth = user
		if password != "" {
			auth += ":" + password
		}
		auth += "@"
	}

	dsn := fmt.Sprintf("%stcp(%s)/%s", auth, net.JoinHostPort(host, strconv.Itoa(port)), name)
	query := params.Encode()
	if query != "" {
		dsn += "?" + query
	}
	return dsn
}

func (c RedisRuntimeConfig) URLValue() string {
	if u := normalizeRedisRawURL(c.URL); u != "" {
		return u
	}

	host := strings.TrimSpace(c.Host)
	if host == "" {
		host = defaultRedisHost
	}
	port := c.Port
	if port == 0 {
		port = defaultRedisPort
	}
	db := c.DB
	if db < 0 {
		db = defaultRedisDB
	}

	scheme := strings.ToLower(strings.TrimSpace(c.Scheme))
	if scheme == "" {
		if c.TLS {
			scheme = "rediss"
		} else {
			scheme = "redis"
		}
	}
	if scheme != "redis" && scheme != "rediss" {
		scheme = "redis"
	}

	u := &neturl.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   "/" + strconv.Itoa(db),
	}
	username := strings.TrimSpace(c.Username)
	password := strings.TrimSpace(c.Password)
	if username != "" {
		if password != "" {
			u.User = neturl.UserPassword(username, password)
		} else {
			u.User = neturl.User(username)
		}
	} else if password != "" {
		u.User = neturl.UserPassword("", password)
	}

	if len(c.Params) > 0 {
		query := neturl.Values{}
		for key, value := range c.Params {
			k := strings.TrimSpace(key)
			v := strings.TrimSpace(value)
			if k != "" && v != "" {
				query.Set(k, v)
			}
		}
		if len(query) > 0 {
			u.RawQuery = query.Encode()
		}
	}

	return u.String()
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

func (c MeiliSearchRuntimeConfig) Endpoint() string {
	if c.URL != "" {
		if strings.HasPrefix(c.URL, "http://") || strings.HasPrefix(c.URL, "https://") {
			return c.URL
		}
		return "http://" + c.URL
	}

	host := c.Host
	if host == "" {
		host = defaultMeiliHost
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return strings.TrimRight(host, "/")
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return "http://" + host
	}

	port := c.Port
	if port == 0 {
		port = defaultMeiliPort
	}
	return fmt.Sprintf("http://%s:%d", host, port)
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

func (c *AppConfig) IsDev() bool {
	return strings.EqualFold(c.Env, defaultEnv)
}

func (c *AppConfig) AdminAssetPath() string {
	return normalizeAdminAssetPath(c.MXAdmin)
}

// FullConfig is the application config stored in the database (options table, key="configs").
// It mirrors the TypeScript ConfigsSchema exactly.
type FullConfig struct {
	SEO                          SEOConfig                    `json:"seo"`
	URL                          URLConfig                    `json:"url"`
	MailOptions                  MailOptions                  `json:"mail_options"`
	CommentOptions               CommentOptions               `json:"comment_options"`
	BackupOptions                BackupOptions                `json:"backup_options"`
	BaiduSearchOptions           BaiduSearchOptions           `json:"baidu_search_options"`
	AlgoliaSearchOptions         AlgoliaSearchOptions         `json:"algolia_search_options"`
	AdminExtra                   AdminExtra                   `json:"admin_extra"`
	FriendLinkOptions            FriendLinkOptions            `json:"friend_link_options"`
	S3Options                    S3Options                    `json:"s3_options"`
	ImageBedOptions              ImageBedOptions              `json:"image_bed_options"`
	ImageStorageOptions          ImageStorageOptions          `json:"image_storage_options"`
	ThirdPartyServiceIntegration ThirdPartyServiceIntegration `json:"third_party_service_integration"`
	TextOptions                  TextOptions                  `json:"text_options"`
	BingSearchOptions            BingSearchOptions            `json:"bing_search_options"`
	MeiliSearchOptions           MeiliSearchOptions           `json:"meili_search_options"`
	FeatureList                  FeatureList                  `json:"feature_list"`
	BarkOptions                  BarkOptions                  `json:"bark_options"`
	AuthSecurity                 AuthSecurity                 `json:"auth_security"`
	AI                           AIConfig                     `json:"ai"`
	OAuth                        OAuthConfig                  `json:"oauth"`
}

type SEOConfig struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Keywords    []string `json:"keywords"`
}

type URLConfig struct {
	WebURL    string `json:"web_url"`
	AdminURL  string `json:"admin_url"`
	ServerURL string `json:"server_url"`
	WSURL     string `json:"ws_url"`
}

type MailOptions struct {
	Provider string        `json:"provider"` // smtp | resend
	Enable   bool          `json:"enable"`
	From     string        `json:"from"`
	SMTP     *SMTPConfig   `json:"smtp,omitempty"`
	Resend   *ResendConfig `json:"resend,omitempty"`
}

type SMTPConfig struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Secure bool   `json:"secure"`
	Auth   struct {
		User string `json:"user"`
		Pass string `json:"pass"`
	} `json:"auth"`
}

type ResendConfig struct {
	APIKey string `json:"api_key"`
}

type CommentOptions struct {
	AntiSpam           bool     `json:"anti_spam"`
	AIReview           bool     `json:"ai_review"`
	AIReviewType       string   `json:"ai_review_type"` // binary | score
	AIReviewThreshold  int      `json:"ai_review_threshold"`
	DisableComment     bool     `json:"disable_comment"`
	SpamKeywords       []string `json:"spam_keywords"`
	BlockIPs           []string `json:"block_ips"`
	DisableNoChinese   bool     `json:"disable_no_chinese"`
	CommentShouldAudit bool     `json:"comment_should_audit"`
	RecordIPLocation   bool     `json:"record_ip_location"`
}

type BackupOptions struct {
	Enable bool   `json:"enable"`
	Path   string `json:"path"`
}

type BaiduSearchOptions struct {
	Enable bool   `json:"enable"`
	Token  string `json:"token"`
}

type AlgoliaSearchOptions struct {
	Enable    bool   `json:"enable"`
	AppID     string `json:"app_id"`
	APIKey    string `json:"api_key"`
	IndexName string `json:"index_name"`
}

type AdminExtra struct {
	EnableAdminProxy bool    `json:"enable_admin_proxy"`
	GaodeMapKey      *string `json:"gaodemap_key"`
	Background       string  `json:"background,omitempty"`
	WalineServerURL  string  `json:"waline_server_url,omitempty"`
}

type FriendLinkOptions struct {
	AllowApply            bool `json:"allow_apply"`
	AllowSubPath          bool `json:"allow_sub_path"`
	AvatarInternalization bool `json:"avatar_internationalization"`
}

type S3Options struct {
	Endpoint        string `json:"endpoint"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	Bucket          string `json:"bucket"`
	Region          string `json:"region"`
	CustomDomain    string `json:"custom_domain"`
	PathStyleAccess bool   `json:"path_style_access"`
}

type ImageBedOptions struct {
	Enable         bool     `json:"enable"`
	Path           string   `json:"path"`
	AllowedFormats []string `json:"allowed_formats"`
	MaxSize        int      `json:"max_size"`
}

type ImageStorageOptions struct {
	Enable              bool `json:"enable"`
	AutoDeleteAfterSync bool `json:"auto_delete_after_sync"`
}

type ThirdPartyServiceIntegration struct {
	GitHubToken string `json:"github_token"`
}

type TextOptions struct {
	MacroEnabled bool `json:"macro_enabled"`
}

type BingSearchOptions struct {
	Enable bool   `json:"enable"`
	Token  string `json:"token"`
}

type MeiliSearchOptions struct {
	Enable         bool   `json:"enable"`
	Host           string `json:"host"`
	APIKey         string `json:"api_key"`
	IndexName      string `json:"index_name"`
	SearchCacheTTL int    `json:"search_cache_ttl"`
}

type FeatureList struct {
	FriendlyCommentEditorEnabled bool `json:"friendly_comment_editor_enabled"`
}

type BarkOptions struct {
	Enable    bool   `json:"enable"`
	Key       string `json:"key"`
	ServerURL string `json:"server_url"`
}

type AuthSecurity struct {
	DisablePasswordLogin bool `json:"disable_password_login"`
}

type AIConfig struct {
	Providers                 []AIProvider `json:"providers"`
	SummaryModel              string       `json:"summary_model"`
	CommentReviewModel        string       `json:"comment_review_model"`
	EnableSummary             bool         `json:"enable_summary"`
	EnableAutoGenerateSummary bool         `json:"enable_auto_generate_summary"`
	AISummaryTargetLanguage   string       `json:"ai_summary_target_language"`
}

type AIProvider struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"` // OpenAI | OpenAI-Compatible | Anthropic | OpenRouter
	APIKey       string `json:"api_key"`
	Endpoint     string `json:"endpoint,omitempty"`
	DefaultModel string `json:"default_model"`
	Enabled      bool   `json:"enabled"`
}

type OAuthConfig struct {
	Providers []OAuthProvider        `json:"providers"`
	Secrets   map[string]interface{} `json:"secrets"`
	Public    map[string]interface{} `json:"public"`
}

type OAuthProvider struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Enabled      bool   `json:"enabled"`
}

// DefaultFullConfig returns sensible defaults matching the original TypeScript defaults.
func DefaultFullConfig() FullConfig {
	return FullConfig{
		SEO: SEOConfig{
			Title:       "我的小世界呀",
			Description: "哈喽~欢迎光临",
			Keywords:    []string{},
		},
		CommentOptions: CommentOptions{
			RecordIPLocation:  true,
			AIReviewType:      "binary",
			AIReviewThreshold: 5,
		},
		AdminExtra: AdminExtra{
			EnableAdminProxy: true,
		},
		MeiliSearchOptions: MeiliSearchOptions{
			Enable:         true,
			IndexName:      "mx-space",
			SearchCacheTTL: 300,
		},
		AI: AIConfig{
			Providers:               []AIProvider{},
			AISummaryTargetLanguage: "auto",
		},
		OAuth: OAuthConfig{
			Providers: []OAuthProvider{},
			Secrets:   map[string]interface{}{},
			Public:    map[string]interface{}{},
		},
	}
}
