package config

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
