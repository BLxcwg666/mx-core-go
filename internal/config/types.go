package config

// AppConfig holds runtime startup configuration loaded from YAML.
type AppConfig struct {
	Port           int                      `yaml:"port"`
	DSN            string                   `yaml:"dsn"` // MySQL DSN
	RedisURL       string                   `yaml:"redis_url"`
	Database       DatabaseRuntimeConfig    `yaml:"database"`
	Redis          RedisRuntimeConfig       `yaml:"redis"`
	Env            string                   `yaml:"env"` // "development" | "production"
	Cluster        bool                     `yaml:"cluster"`
	ClusterWorkers int                      `yaml:"cluster_workers"`
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
	Cluster            *bool                `yaml:"cluster"`
	ClusterWorkers     int                  `yaml:"cluster_workers"`
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
