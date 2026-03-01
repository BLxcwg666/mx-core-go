package config

import (
	"bytes"
	"encoding/json"
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
	Paths          RuntimePathsConfig       `yaml:"paths"`
	LogRotateSize  *int                     `yaml:"log_rotate_size_mb"`
	LogRotateKeep  *int                     `yaml:"log_rotate_keep"`
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

type RuntimePathsConfig struct {
	Logs    string `yaml:"logs"`
	Backups string `yaml:"backups"`
	Static  string `yaml:"static"`
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
	Paths              rawPathsConfig       `yaml:"paths"`
	LogDir             string               `yaml:"log_dir"`
	LogsDir            string               `yaml:"logs_dir"`
	LogRotateSize      *int                 `yaml:"log_rotate_size_mb"`
	LogRotateKeep      *int                 `yaml:"log_rotate_keep"`
	BackupDir          string               `yaml:"backup_dir"`
	BackupsDir         string               `yaml:"backups_dir"`
	StaticDir          string               `yaml:"static_dir"`
	StaticsDir         string               `yaml:"statics_dir"`
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

type rawPathsConfig struct {
	Logs    string `yaml:"logs"`
	Backups string `yaml:"backups"`
	Static  string `yaml:"static"`
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

func normalizeRuntimePaths(paths RuntimePathsConfig) RuntimePathsConfig {
	paths.Logs = strings.TrimSpace(paths.Logs)
	paths.Backups = strings.TrimSpace(paths.Backups)
	paths.Static = strings.TrimSpace(paths.Static)
	return paths
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
	Enable   bool          `json:"enable"`
	Provider string        `json:"provider"`
	From     string        `json:"from"`
	SMTP     *SMTPConfig   `json:"smtp"`
	Resend   *ResendConfig `json:"resend"`
}

type SMTPOptions struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Secure bool   `json:"secure"`
}

type SMTPConfig struct {
	User    string      `json:"user"`
	Pass    string      `json:"pass"`
	Options SMTPOptions `json:"options"`
}

func (s SMTPConfig) MarshalJSON() ([]byte, error) {
	host := strings.TrimSpace(s.Options.Host)
	port := s.Options.Port
	if port == 0 {
		port = 465
	}
	secure := s.Options.Secure

	return json.Marshal(struct {
		User    string      `json:"user"`
		Pass    string      `json:"pass"`
		Host    string      `json:"host"`
		Port    int         `json:"port"`
		Secure  bool        `json:"secure"`
		Options SMTPOptions `json:"options"`
	}{
		User:   strings.TrimSpace(s.User),
		Pass:   s.Pass,
		Host:   host,
		Port:   port,
		Secure: secure,
		Options: SMTPOptions{
			Host:   host,
			Port:   port,
			Secure: secure,
		},
	})
}

type ResendConfig struct {
	APIKey string `json:"api_key"`
}

type CommentOptions struct {
	AntiSpam           bool     `json:"anti_spam"`
	AIReview           bool     `json:"ai_review"`
	AIReviewType       string   `json:"ai_review_type"` // binary | score
	AIReviewThreshold  int      `json:"ai_review_threshold"`
	TestAIReview       string   `json:"test_ai_review"`
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
	Enable bool    `json:"enable"`
	Token  *string `json:"token"`
}

type AlgoliaSearchOptions struct {
	Enable          bool   `json:"enable"`
	AppID           string `json:"app_id"`
	APIKey          string `json:"api_key"`
	IndexName       string `json:"index_name"`
	MaxTruncateSize int    `json:"max_truncate_size"`
}

type AdminExtra struct {
	EnableAdminProxy bool    `json:"enable_admin_proxy"`
	GaodeMapKey      *string `json:"gaodemap_key"`
	Background       string  `json:"background"`
	WalineServerURL  string  `json:"waline_server_url,omitempty"`
}

type FriendLinkOptions struct {
	AllowApply                  bool `json:"allow_apply"`
	AllowSubPath                bool `json:"allow_sub_path"`
	EnableAvatarInternalization bool `json:"enable_avatar_internalization"`
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
	Enable         bool   `json:"enable"`
	Path           string `json:"path"`
	AllowedFormats string `json:"allowed_formats"`
	MaxSizeMB      int    `json:"max_size_mb"`
}

type ImageStorageOptions struct {
	Enable               bool    `json:"enable"`
	SyncOnPublish        bool    `json:"sync_on_publish"`
	DeleteLocalAfterSync bool    `json:"delete_local_after_sync"`
	Endpoint             *string `json:"endpoint"`
	SecretID             *string `json:"secret_id"`
	SecretKey            *string `json:"secret_key"`
	Bucket               *string `json:"bucket"`
	Region               string  `json:"region"`
	CustomDomain         string  `json:"custom_domain"`
	Prefix               string  `json:"prefix"`
}

type ThirdPartyServiceIntegration struct {
	GitHubToken string `json:"github_token"`
}

type TextOptions struct {
	Macros bool `json:"macros"`
}

type BingSearchOptions struct {
	Enable bool    `json:"enable"`
	Token  *string `json:"token"`
}

type MeiliSearchOptions struct {
	Enable         bool   `json:"enable"`
	Host           string `json:"host,omitempty"`
	APIKey         string `json:"api_key,omitempty"`
	IndexName      string `json:"index_name"`
	SearchCacheTTL int    `json:"search_cache_ttl"`
}

type FeatureList struct {
	EmailSubscribe bool `json:"email_subscribe"`
}

type BarkOptions struct {
	Enable              bool   `json:"enable"`
	Key                 string `json:"key"`
	ServerURL           string `json:"server_url"`
	EnableComment       bool   `json:"enable_comment"`
	EnableThrottleGuard bool   `json:"enable_throttle_guard"`
}

type AuthSecurity struct {
	DisablePasswordLogin bool `json:"disable_password_login"`
}

type AIConfig struct {
	Providers                 []AIProvider       `json:"providers"`
	SummaryModel              *AIModelAssignment `json:"summary_model,omitempty"`
	CommentReviewModel        *AIModelAssignment `json:"comment_review_model,omitempty"`
	EnableSummary             bool               `json:"enable_summary"`
	EnableAutoGenerateSummary bool               `json:"enable_auto_generate_summary"`
	AISummaryTargetLanguage   string             `json:"ai_summary_target_language"`
}

type AIModelAssignment struct {
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
}

func (s *SMTPConfig) UnmarshalJSON(data []byte) error {
	next := *s
	if next.Options.Port == 0 {
		next.Options.Port = 465
	}

	var raw struct {
		User    string `json:"user"`
		Pass    string `json:"pass"`
		Options *struct {
			Host   string `json:"host"`
			Port   int    `json:"port"`
			Secure *bool  `json:"secure"`
		} `json:"options"`
		Host   string `json:"host"`
		Port   int    `json:"port"`
		Secure *bool  `json:"secure"`
		Auth   *struct {
			User string `json:"user"`
			Pass string `json:"pass"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.User != "" {
		next.User = strings.TrimSpace(raw.User)
	}
	if raw.Pass != "" {
		next.Pass = raw.Pass
	}
	if raw.Auth != nil {
		if next.User == "" {
			next.User = strings.TrimSpace(raw.Auth.User)
		}
		if next.Pass == "" {
			next.Pass = raw.Auth.Pass
		}
	}

	if raw.Options != nil {
		next.Options.Host = strings.TrimSpace(raw.Options.Host)
		if raw.Options.Port != 0 {
			next.Options.Port = raw.Options.Port
		}
		if raw.Options.Secure != nil {
			next.Options.Secure = *raw.Options.Secure
		}
	} else {
		if strings.TrimSpace(raw.Host) != "" {
			next.Options.Host = strings.TrimSpace(raw.Host)
		}
		if raw.Port != 0 {
			next.Options.Port = raw.Port
		}
		if raw.Secure != nil {
			next.Options.Secure = *raw.Secure
		}
	}

	if next.Options.Port == 0 {
		next.Options.Port = 465
	}
	*s = next
	return nil
}

func (o *FriendLinkOptions) UnmarshalJSON(data []byte) error {
	next := *o
	var raw struct {
		AllowApply                  *bool `json:"allow_apply"`
		AllowSubPath                *bool `json:"allow_sub_path"`
		EnableAvatarInternalization *bool `json:"enable_avatar_internalization"`
		AvatarInternationalization  *bool `json:"avatar_internationalization"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.AllowApply != nil {
		next.AllowApply = *raw.AllowApply
	}
	if raw.AllowSubPath != nil {
		next.AllowSubPath = *raw.AllowSubPath
	}
	if raw.EnableAvatarInternalization != nil {
		next.EnableAvatarInternalization = *raw.EnableAvatarInternalization
	} else if raw.AvatarInternationalization != nil {
		next.EnableAvatarInternalization = *raw.AvatarInternationalization
	}

	*o = next
	return nil
}

func (o *ImageBedOptions) UnmarshalJSON(data []byte) error {
	next := *o
	var raw struct {
		Enable         *bool       `json:"enable"`
		Path           *string     `json:"path"`
		AllowedFormats interface{} `json:"allowed_formats"`
		MaxSizeMB      *int        `json:"max_size_mb"`
		MaxSize        *int        `json:"max_size"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.Enable != nil {
		next.Enable = *raw.Enable
	}
	if raw.Path != nil {
		next.Path = *raw.Path
	}
	if raw.AllowedFormats != nil {
		switch val := raw.AllowedFormats.(type) {
		case string:
			next.AllowedFormats = strings.TrimSpace(val)
		case []interface{}:
			items := make([]string, 0, len(val))
			for _, item := range val {
				s, ok := item.(string)
				if !ok {
					continue
				}
				s = strings.TrimSpace(s)
				if s == "" {
					continue
				}
				items = append(items, s)
			}
			next.AllowedFormats = strings.Join(items, ",")
		}
	}
	if raw.MaxSizeMB != nil {
		next.MaxSizeMB = *raw.MaxSizeMB
	} else if raw.MaxSize != nil {
		next.MaxSizeMB = *raw.MaxSize
	}

	*o = next
	return nil
}

func (o *ImageStorageOptions) UnmarshalJSON(data []byte) error {
	next := *o
	var raw struct {
		Enable               *bool   `json:"enable"`
		SyncOnPublish        *bool   `json:"sync_on_publish"`
		DeleteLocalAfterSync *bool   `json:"delete_local_after_sync"`
		AutoDeleteAfterSync  *bool   `json:"auto_delete_after_sync"`
		Endpoint             *string `json:"endpoint"`
		SecretID             *string `json:"secret_id"`
		SecretKey            *string `json:"secret_key"`
		Bucket               *string `json:"bucket"`
		Region               *string `json:"region"`
		CustomDomain         *string `json:"custom_domain"`
		Prefix               *string `json:"prefix"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.Enable != nil {
		next.Enable = *raw.Enable
	}
	if raw.SyncOnPublish != nil {
		next.SyncOnPublish = *raw.SyncOnPublish
	}
	if raw.DeleteLocalAfterSync != nil {
		next.DeleteLocalAfterSync = *raw.DeleteLocalAfterSync
	} else if raw.AutoDeleteAfterSync != nil {
		next.DeleteLocalAfterSync = *raw.AutoDeleteAfterSync
	}
	if raw.Endpoint != nil {
		next.Endpoint = raw.Endpoint
	}
	if raw.SecretID != nil {
		next.SecretID = raw.SecretID
	}
	if raw.SecretKey != nil {
		next.SecretKey = raw.SecretKey
	}
	if raw.Bucket != nil {
		next.Bucket = raw.Bucket
	}
	if raw.Region != nil {
		next.Region = *raw.Region
	}
	if raw.CustomDomain != nil {
		next.CustomDomain = *raw.CustomDomain
	}
	if raw.Prefix != nil {
		next.Prefix = *raw.Prefix
	}

	*o = next
	return nil
}

func (o *TextOptions) UnmarshalJSON(data []byte) error {
	next := *o
	var raw struct {
		Macros       *bool `json:"macros"`
		MacroEnabled *bool `json:"macro_enabled"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.Macros != nil {
		next.Macros = *raw.Macros
	} else if raw.MacroEnabled != nil {
		next.Macros = *raw.MacroEnabled
	}

	*o = next
	return nil
}

func (o *FeatureList) UnmarshalJSON(data []byte) error {
	next := *o
	var raw struct {
		EmailSubscribe               *bool `json:"email_subscribe"`
		FriendlyCommentEditorEnabled *bool `json:"friendly_comment_editor_enabled"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.EmailSubscribe != nil {
		next.EmailSubscribe = *raw.EmailSubscribe
	} else if raw.FriendlyCommentEditorEnabled != nil {
		next.EmailSubscribe = *raw.FriendlyCommentEditorEnabled
	}

	*o = next
	return nil
}

func (a *AIModelAssignment) UnmarshalJSON(data []byte) error {
	var raw struct {
		ProviderID      string `json:"provider_id"`
		ProviderIDCamel string `json:"providerId"`
		Model           string `json:"model"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	a.ProviderID = strings.TrimSpace(raw.ProviderID)
	if a.ProviderID == "" {
		a.ProviderID = strings.TrimSpace(raw.ProviderIDCamel)
	}
	a.Model = strings.TrimSpace(raw.Model)
	return nil
}

func (a *AIConfig) UnmarshalJSON(data []byte) error {
	next := *a
	var raw struct {
		Providers                 []AIProvider    `json:"providers"`
		SummaryModel              json.RawMessage `json:"summary_model"`
		CommentReviewModel        json.RawMessage `json:"comment_review_model"`
		EnableSummary             *bool           `json:"enable_summary"`
		EnableAutoGenerateSummary *bool           `json:"enable_auto_generate_summary"`
		AISummaryTargetLanguage   *string         `json:"ai_summary_target_language"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.Providers != nil {
		next.Providers = raw.Providers
	}
	if raw.EnableSummary != nil {
		next.EnableSummary = *raw.EnableSummary
	}
	if raw.EnableAutoGenerateSummary != nil {
		next.EnableAutoGenerateSummary = *raw.EnableAutoGenerateSummary
	}
	if raw.AISummaryTargetLanguage != nil {
		next.AISummaryTargetLanguage = *raw.AISummaryTargetLanguage
	}

	var err error
	if len(raw.SummaryModel) > 0 {
		next.SummaryModel, err = parseAIModelAssignment(raw.SummaryModel, next.SummaryModel)
		if err != nil {
			return err
		}
	}
	if len(raw.CommentReviewModel) > 0 {
		next.CommentReviewModel, err = parseAIModelAssignment(raw.CommentReviewModel, next.CommentReviewModel)
		if err != nil {
			return err
		}
	}

	*a = next
	return nil
}

func parseAIModelAssignment(raw json.RawMessage, fallback *AIModelAssignment) (*AIModelAssignment, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return fallback, nil
	}
	if trimmed == "null" {
		return nil, nil
	}

	var legacyModel string
	if err := json.Unmarshal(raw, &legacyModel); err == nil {
		legacyModel = strings.TrimSpace(legacyModel)
		if legacyModel == "" {
			return nil, nil
		}
		next := &AIModelAssignment{}
		if fallback != nil {
			*next = *fallback
		}
		next.Model = legacyModel
		return next, nil
	}

	next := &AIModelAssignment{}
	if fallback != nil {
		*next = *fallback
	}
	if err := json.Unmarshal(raw, next); err != nil {
		return nil, err
	}
	if strings.TrimSpace(next.ProviderID) == "" && strings.TrimSpace(next.Model) == "" {
		return nil, nil
	}
	return next, nil
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
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
}

func (p *OAuthProvider) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.Type = strings.TrimSpace(raw.Type)
	if p.Type == "" {
		p.Type = strings.TrimSpace(raw.ID)
	}
	p.Enabled = raw.Enabled
	return nil
}

func (p OAuthProvider) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type    string `json:"type"`
		Enabled bool   `json:"enabled"`
	}{
		Type:    strings.TrimSpace(p.Type),
		Enabled: p.Enabled,
	})
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
