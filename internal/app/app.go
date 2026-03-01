package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/database"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/ack"
	"github.com/mx-space/core/internal/modules/activity"
	"github.com/mx-space/core/internal/modules/aggregate"
	"github.com/mx-space/core/internal/modules/ai"
	"github.com/mx-space/core/internal/modules/analyze"
	"github.com/mx-space/core/internal/modules/auth"
	"github.com/mx-space/core/internal/modules/authn"
	"github.com/mx-space/core/internal/modules/backup"
	"github.com/mx-space/core/internal/modules/category"
	"github.com/mx-space/core/internal/modules/comment"
	appconfigs "github.com/mx-space/core/internal/modules/configs"
	"github.com/mx-space/core/internal/modules/crontask"
	"github.com/mx-space/core/internal/modules/debug"
	"github.com/mx-space/core/internal/modules/dependency"
	"github.com/mx-space/core/internal/modules/draft"
	"github.com/mx-space/core/internal/modules/feed"
	"github.com/mx-space/core/internal/modules/file"
	"github.com/mx-space/core/internal/modules/gateway"
	"github.com/mx-space/core/internal/modules/health"
	"github.com/mx-space/core/internal/modules/helper"
	init_ "github.com/mx-space/core/internal/modules/init"
	"github.com/mx-space/core/internal/modules/link"
	"github.com/mx-space/core/internal/modules/markdown"
	"github.com/mx-space/core/internal/modules/metapreset"
	"github.com/mx-space/core/internal/modules/note"
	"github.com/mx-space/core/internal/modules/option"
	"github.com/mx-space/core/internal/modules/page"
	"github.com/mx-space/core/internal/modules/pageproxy"
	"github.com/mx-space/core/internal/modules/post"
	"github.com/mx-space/core/internal/modules/project"
	"github.com/mx-space/core/internal/modules/pty"
	"github.com/mx-space/core/internal/modules/reader"
	"github.com/mx-space/core/internal/modules/recently"
	"github.com/mx-space/core/internal/modules/render"
	"github.com/mx-space/core/internal/modules/say"
	"github.com/mx-space/core/internal/modules/search"
	"github.com/mx-space/core/internal/modules/serverless"
	"github.com/mx-space/core/internal/modules/servertime"
	"github.com/mx-space/core/internal/modules/sitemap"
	"github.com/mx-space/core/internal/modules/slugtracker"
	"github.com/mx-space/core/internal/modules/snippet"
	"github.com/mx-space/core/internal/modules/subscribe"
	"github.com/mx-space/core/internal/modules/topic"
	"github.com/mx-space/core/internal/modules/update"
	"github.com/mx-space/core/internal/modules/user"
	"github.com/mx-space/core/internal/modules/webhook"
	"github.com/mx-space/core/internal/pkg/bark"
	pkgcron "github.com/mx-space/core/internal/pkg/cron"
	jwtpkg "github.com/mx-space/core/internal/pkg/jwt"
	"github.com/mx-space/core/internal/pkg/nativelog"
	pkgredis "github.com/mx-space/core/internal/pkg/redis"
	"github.com/mx-space/core/internal/pkg/taskqueue"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

	db, err := database.Connect(cfg)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}

	rc, err := pkgredis.Connect(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("redis: %w", err)
	}

	if !cfg.IsDev() {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(middleware.Logger(logger))

	corsConfig := cors.Config{
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
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
	registerCronJobs(sched, db)
	go sched.Start(ctx)

	app := &App{cfg: cfg, router: router, db: db, hub: hub, logger: logger, cancel: cancel, sched: sched}
	app.registerRoutes(rc)

	return app, nil
}

// registerCronJobs registers all scheduled background jobs.
func registerCronJobs(sched *pkgcron.Scheduler, db *gorm.DB) {
	sched.Register(pkgcron.Job{
		Name:        "cleanup_analytics",
		Description: "清理 90 天以上的访问记录",
		Interval:    24 * time.Hour,
		Fn: func(ctx context.Context) error {
			cutoff := time.Now().AddDate(0, 0, -90)
			return db.Where("created_at < ?", cutoff).Delete(&models.AnalyzeModel{}).Error
		},
	})

	sched.Register(pkgcron.Job{
		Name:        "check_links",
		Description: "检查友链可用性",
		Interval:    12 * time.Hour,
		Fn: func(ctx context.Context) error {
			svc := link.NewService(db)
			results := svc.HealthCheck()
			for _, r := range results {
				if r.Status == 0 || r.Status >= 400 {
					db.Model(&models.LinkModel{}).
						Where("id = ? AND state = ?", r.ID, models.LinkPass).
						Update("state", models.LinkOutdate)
				}
			}
			return nil
		},
	})

	sched.Register(pkgcron.Job{
		Name:        "auto_backup",
		Description: "自动备份数据库到本地",
		Interval:    24 * time.Hour,
		Fn: func(ctx context.Context) error {
			return backup.CreateLocalBackup(db)
		},
	})
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

func (a *App) registerRoutes(rc *pkgredis.Client) {
	r := a.router
	db := a.db
	authMW := middleware.Auth(db)

	appInfo := gin.H{
		"name":     "mx-space-core",
		"author":   "libxcnya.so <me@xcnya.cn> / Innei <https://innei.in>",
		"version":  "1.0.0",
		"homepage": "https://github.com/BLxcwg666/mx-core-go",
		"issues":   "https://github.com/BLxcwg666/mx-core-go/issues",
	}

	r.Use(analyze.Middleware(db))

	apiPrefix := "/api/v2"

	// Shared services
	cfgSvc := appconfigs.NewService(db)
	searchSvc := search.NewService(db, cfgSvc, a.cfg)

	// Bark push service for rate-limit alerts.
	barkSvc := bark.New(func() (key, serverURL, siteTitle string) {
		cfg, err := cfgSvc.Get()
		if err != nil {
			return "", "", ""
		}
		return cfg.BarkOptions.Key, cfg.BarkOptions.ServerURL, cfg.SEO.Title
	})

	// Rate limiting and idempotence run on every route (requires Redis).
	r.Use(middleware.RateLimit(rc.Raw(), barkSvc))
	r.Use(middleware.Idempotence(rc.Raw()))

	taskSvc := taskqueue.NewService(rc)

	// Root-level endpoints
	root := r.Group("")
	sitemap.RegisterRoutes(root, db, cfgSvc)
	feed.RegisterRoutes(root, db, cfgSvc) // /feed.xml, /atom.xml
	render.NewHandler(db).RegisterRoutes(root, authMW)
	pageproxy.NewHandler(cfgSvc, a.cfg).RegisterRoutes(root)

	// Versioned API
	api := r.Group(apiPrefix)
	api.Use(middleware.OptionalAuth(db))

	// Infrastructure
	health.RegisterRoutes(api, db, a.sched, cfgSvc, authMW)
	aggregate.RegisterRoutes(api, db, cfgSvc, a.hub, rc)
	ack.NewHandler(db, a.hub).RegisterRoutes(api)
	if apiPrefix != "" {
		feed.RegisterRoutes(api, db, cfgSvc) // also at /api/v2/feed
		sitemap.RegisterRoutes(api, db, cfgSvc)
	}
	servertime.RegisterRoutes(api)

	// Init (setup wizard)
	init_.NewHandler(db, cfgSvc).RegisterRoutes(api)

	// App info endpoint
	api.GET("", func(c *gin.Context) { c.PureJSON(http.StatusOK, appInfo) })
	api.GET("/info", func(c *gin.Context) { c.PureJSON(http.StatusOK, appInfo) })
	api.GET("/ping", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"data": "pong"}) })
	api.GET("/uptime", func(c *gin.Context) {
		uptimeMs := time.Since(a.cfgStartTime()).Milliseconds()
		c.JSON(http.StatusOK, gin.H{
			"timestamp": uptimeMs,
			"humanize":  humanizeDuration(time.Duration(uptimeMs) * time.Millisecond),
		})
	})

	api.GET("/like_this", func(c *gin.Context) {
		var opt models.OptionModel
		if err := db.Where("name = ?", "like").First(&opt).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusOK, 0)
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"ok": 0, "code": http.StatusInternalServerError, "message": err.Error()})
			return
		}
		n, err := strconv.ParseInt(strings.TrimSpace(opt.Value), 10, 64)
		if err != nil {
			c.JSON(http.StatusOK, 0)
			return
		}
		c.JSON(http.StatusOK, n)
	})
	api.POST("/like_this", func(c *gin.Context) {
		ip := c.ClientIP()
		date := time.Now().Format("2006-01-02")
		key := fmt.Sprintf("mx:like_site:%s:%s", date, ip)
		set, err := rc.Raw().SetNX(c.Request.Context(), key, 1, 24*time.Hour).Result()
		if err == nil && !set {
			c.JSON(http.StatusBadRequest, gin.H{"ok": 0, "code": http.StatusBadRequest, "message": "already liked today"})
			return
		}
		err = db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "name"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"value": gorm.Expr("CAST(value AS UNSIGNED) + 1"),
			}),
		}).Create(&models.OptionModel{
			Name:  "like",
			Value: "1",
		}).Error
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": 0, "code": http.StatusInternalServerError, "message": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	})

	cleanCache := func(c *gin.Context) {
		cfgSvc.Invalidate()
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
	api.GET("/clean_cache", authMW, cleanCache)
	api.GET("/clean_catch", authMW, cleanCache) // legacy typo compatibility
	api.GET("/clean_redis", authMW, func(c *gin.Context) {
		rc.Raw().FlushDB(c.Request.Context())
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// Config
	appconfigs.NewHandler(cfgSvc).RegisterRoutes(api, authMW)

	// Auth & User
	auth.NewHandler(auth.NewService(db)).RegisterRoutes(api, authMW)
	auth.NewOAuthHandler(db, cfgSvc).RegisterRoutes(api)
	authn.NewHandler(db).RegisterRoutes(api, authMW)
	user.NewHandler(user.NewService(db), cfgSvc).RegisterRoutes(api, authMW)
	reader.NewHandler(db).RegisterRoutes(api, authMW)

	// Content
	postSvc := post.NewService(db)
	pageSvc := page.NewService(db)
	slugTrackerSvc := slugtracker.NewService(db)
	postSvc.SetSlugTracker(slugTrackerSvc)
	pageSvc.SetSlugTracker(slugTrackerSvc)

	post.NewHandler(postSvc).RegisterRoutes(api, authMW)
	note.NewHandler(note.NewService(db)).RegisterRoutes(api, authMW)
	page.NewHandler(pageSvc).RegisterRoutes(api, authMW)
	recently.NewHandler(recently.NewService(db)).RegisterRoutes(api, authMW)
	draft.NewHandler(draft.NewService(db)).RegisterRoutes(api, authMW)

	// Taxonomy
	category.NewHandler(category.NewService(db)).RegisterRoutes(api, authMW)
	topic.NewHandler(topic.NewService(db)).RegisterRoutes(api, authMW)

	// Comments
	comment.NewHandler(comment.NewService(db)).RegisterRoutes(api, authMW)

	// Extras
	say.NewHandler(say.NewService(db)).RegisterRoutes(api, authMW)
	link.NewHandler(link.NewService(db), cfgSvc).RegisterRoutes(api, authMW)
	subscribe.NewHandler(subscribe.NewService(db)).RegisterRoutes(api, authMW)
	snippet.NewHandler(snippet.NewService(db)).RegisterRoutes(api, authMW)
	project.NewHandler(project.NewService(db)).RegisterRoutes(api, authMW)
	helper.NewHandler(db, cfgSvc).RegisterRoutes(api, authMW)
	activity.NewHandler(db).RegisterRoutes(api, authMW)
	metapreset.NewHandler(db).RegisterRoutes(api, authMW)
	serverless.NewHandler(db, a.hub, rc).RegisterRoutes(api, authMW)
	dependency.NewHandler().RegisterRoutes(api, authMW)
	update.NewHandler().RegisterRoutes(api, authMW)
	debug.NewHandler(a.hub).RegisterRoutes(api, authMW)
	pty.NewHandler().RegisterRoutes(api, authMW)

	// Webhooks
	webhook.NewHandler(webhook.NewService(db)).RegisterRoutes(api, authMW)

	// Markdown import/export
	markdown.NewHandler(db).RegisterRoutes(api, authMW)
	file.NewHandler(db).RegisterRoutes(api, authMW)

	// Backups
	backup.NewHandler(db, cfgSvc, rc).RegisterRoutes(api, authMW)

	// Analytics (admin)
	analyze.NewHandler(db).RegisterRoutes(api, authMW)

	// Options (key-value store)
	option.NewHandler(db).RegisterRoutes(api, authMW)

	// Slug tracker (admin + public redirect)
	slugtracker.NewHandler(slugTrackerSvc).RegisterRoutes(api, authMW)

	// Cron task management (admin)
	crontask.NewHandler(a.sched, taskSvc).RegisterRoutes(api, authMW)

	// Search
	search.NewHandler(searchSvc).RegisterRoutes(api, authMW)

	// WebSocket gateway
	gateway.RegisterRoutes(root, a.hub)
	api.GET("/gateway/stats", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"public": a.hub.ClientCount(gateway.RoomPublic),
			"admin":  a.hub.ClientCount(gateway.RoomAdmin),
			"total":  a.hub.ClientCount(""),
		})
	})

	aiSvc := ai.NewService(db, cfgSvc, taskSvc)
	ai.NewHandler(aiSvc).RegisterRoutes(api, authMW)
}

// cfgStartTime keeps runtime uptime stable across hot paths without extra globals.
func (a *App) cfgStartTime() time.Time {
	return processStart
}

var processStart = time.Now()

func applyRuntimeSettings(cfg *config.AppConfig, logger *zap.Logger) error {
	_ = os.Setenv(nativelog.EnvLogDir, cfg.LogDir())
	if sizeMB, ok := cfg.LogRotateSizeMB(); ok {
		_ = os.Setenv(nativelog.EnvLogRotateSizeMB, strconv.Itoa(sizeMB))
	}
	if keep, ok := cfg.LogRotateKeepCount(); ok {
		_ = os.Setenv(nativelog.EnvLogRotateKeep, strconv.Itoa(keep))
	}
	_ = os.Setenv(backup.EnvBackupDir, cfg.BackupDir())

	if secret := strings.TrimSpace(cfg.JWTSecret); secret != "" {
		jwtpkg.SetSecret(secret)
	} else {
		logger.Warn("jwt_secret is empty, using built-in default secret")
	}

	tz := strings.TrimSpace(cfg.Timezone)
	if tz == "" {
		return nil
	}
	loc, err := parseTimezoneLocation(tz)
	if err != nil {
		return fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	time.Local = loc
	_ = os.Setenv("TZ", tz)
	return nil
}

func parseTimezoneLocation(raw string) (*time.Location, error) {
	tz := strings.TrimSpace(raw)
	if tz == "" {
		return time.Local, nil
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc, nil
	}
	if len(tz) == 6 && (tz[0] == '+' || tz[0] == '-') && tz[3] == ':' {
		h, errH := strconv.Atoi(tz[1:3])
		m, errM := strconv.Atoi(tz[4:6])
		if errH == nil && errM == nil && h <= 23 && m <= 59 {
			offset := h*3600 + m*60
			if tz[0] == '-' {
				offset = -offset
			}
			return time.FixedZone(tz, offset), nil
		}
	}
	return nil, fmt.Errorf("expect IANA zone (e.g. Asia/Shanghai) or UTC offset (e.g. +08:00)")
}

func humanizeDuration(d time.Duration) string {
	if d < time.Minute {
		return d.Truncate(time.Second).String()
	}
	if d < time.Hour {
		return d.Truncate(time.Minute).String()
	}
	if d < 24*time.Hour {
		return d.Truncate(time.Hour).String()
	}
	return d.Truncate(24 * time.Hour).String()
}

// extractOriginHost returns the "host[:port]" portion of an origin URL.
func extractOriginHost(origin string) string {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return origin
	}
	return u.Host
}

// matchOriginPattern reports whether host matches the given wildcard pattern.
func matchOriginPattern(pattern, host string) bool {
	if pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:]
		return strings.HasSuffix(host, suffix)
	}
	if strings.HasSuffix(pattern, ":*") {
		prefix := pattern[:len(pattern)-1]
		return strings.HasPrefix(host, prefix)
	}
	return false
}
