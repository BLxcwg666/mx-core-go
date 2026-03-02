package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/database"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/modules/gateway/gateway"
	"github.com/mx-space/core/internal/pkg/cluster"
	pkgcron "github.com/mx-space/core/internal/pkg/cron"
	"github.com/mx-space/core/internal/pkg/prettylog"
	pkgredis "github.com/mx-space/core/internal/pkg/redis"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// App holds all application dependencies.
type App struct {
	cfg    *config.AppConfig
	router *gin.Engine
	db     *gorm.DB
	hub    *gateway.Hub
	logger *zap.Logger
	cancel context.CancelFunc
	sched  *pkgcron.Scheduler
}

// New initializes the application: config → DB → Redis → routes.
func New(logger *zap.Logger, cfg *config.AppConfig) (*App, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	var err error
	if err := applyRuntimeSettings(cfg, logger); err != nil {
		return nil, err
	}

	if cluster.ShouldLogBootstrap() {
		sysLogger := logger.Named("System")
		logDir := cfg.LogDir()
		backupDir := cfg.BackupDir()
		staticDir := cfg.StaticDir()
		sysLogger.Info(prettylog.Blue(fmt.Sprintf("日志目录已经建好：%s", logDir)))
		sysLogger.Info(prettylog.Blue(fmt.Sprintf("备份目录已经建好：%s", backupDir)))
		sysLogger.Info(prettylog.Blue(fmt.Sprintf("文件存放目录已经建好：%s", staticDir)))
	}

	dbLogger := logger.Named("MySQL")
	if cluster.ShouldLogBootstrap() {
		dbLogger.Info(prettylog.Green("connecting..."))
	}

	db, err := database.Connect(cfg, false)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}

	if cluster.ShouldLogBootstrap() {
		dbLogger.Info(prettylog.Green("readied!"))
	}

	redisLogger := logger.Named("Redis")
	if cluster.ShouldLogBootstrap() {
		redisLogger.Info(prettylog.Green("connecting..."))
	}

	rc, err := pkgredis.Connect(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("redis: %w", err)
	}

	if cluster.ShouldLogBootstrap() {
		redisLogger.Info(prettylog.Green("readied!"))
	}

	if cfg.IsDev() {
		gin.SetMode(gin.DebugMode)
		if !cluster.ShouldLogDevDiagnostics() {
			gin.DebugPrintRouteFunc = func(string, string, string, int) {}
			gin.DebugPrintFunc = func(string, ...interface{}) {}
		}
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	gin.DefaultWriter = devNullWriter{}
	gin.DefaultErrorWriter = os.Stderr

	router := gin.New()
	router.HandleMethodNotAllowed = true
	router.Use(gin.Recovery())
	router.Use(middleware.Logger(logger))

	corsConfig := cors.Config{
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders: []string{
			"Origin",
			"Accept",
			"Content-Length",
			"Content-Type",
			"Authorization",
			"X-Requested-With",
			"X-Lang",
			"x-lang",
			"X-UUID",
			"x-uuid",
			"X-Session-UUID",
			"x-session-uuid",
		},
		ExposeHeaders:    []string{"Content-Length", "x-mx-cache", "x-mx-served-by"},
		AllowCredentials: true,
	}
	if len(cfg.AllowedOrigins) > 0 && !cfg.IsDev() {
		patterns := cfg.AllowedOrigins
		corsConfig.AllowOriginFunc = func(origin string) bool {
			host := extractOriginHost(origin)
			for _, pattern := range patterns {
				if matchOriginPattern(pattern, host) {
					return true
				}
			}
			return false
		}
	} else {
		corsConfig.AllowOriginFunc = func(origin string) bool { return true }
	}
	router.Use(cors.New(corsConfig))

	hub := gateway.NewHub(rc, logger, func(token string) bool {
		_, err := middleware.ValidateToken(db, token)
		return err == nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)

	sched := pkgcron.New()
	if cluster.ShouldRunCron() {
		registerCronJobs(sched, db, cfg, logger)
		go sched.Start(ctx)
	}

	app := &App{cfg: cfg, router: router, db: db, hub: hub, logger: logger, cancel: cancel, sched: sched}
	app.registerRoutes(rc)

	return app, nil
}

// Addr returns the listen address.
func (a *App) Addr() string { return fmt.Sprintf(":%d", a.cfg.Port) }

// Router returns the HTTP handler.
func (a *App) Router() http.Handler { return a.router }

// Shutdown cleans up background goroutines.
func (a *App) Shutdown() { a.cancel() }

func (a *App) AdminProxyPath() string {
	return "/proxy/qaqdmin"
}

func (a *App) AdminProxyDevPath() string {
	return "/proxy/qaqdmin/dev-proxy"
}

// cfgStartTime keeps runtime uptime stable across hot paths without extra globals.
func (a *App) cfgStartTime() time.Time {
	return processStart
}

var processStart = time.Now()

// devNullWriter discards all writes (used to suppress Gin's debug output).
type devNullWriter struct{}

func (devNullWriter) Write(p []byte) (int, error) { return len(p), nil }
