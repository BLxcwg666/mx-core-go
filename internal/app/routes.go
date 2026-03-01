package app

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/auth/auth"
	"github.com/mx-space/core/internal/modules/auth/authn"
	"github.com/mx-space/core/internal/modules/auth/user"
	"github.com/mx-space/core/internal/modules/content/activity"
	"github.com/mx-space/core/internal/modules/content/category"
	"github.com/mx-space/core/internal/modules/content/comment"
	"github.com/mx-space/core/internal/modules/content/draft"
	"github.com/mx-space/core/internal/modules/content/link"
	"github.com/mx-space/core/internal/modules/content/note"
	"github.com/mx-space/core/internal/modules/content/page"
	"github.com/mx-space/core/internal/modules/content/post"
	"github.com/mx-space/core/internal/modules/content/snippet"
	"github.com/mx-space/core/internal/modules/content/topic"
	"github.com/mx-space/core/internal/modules/gateway/gateway"
	"github.com/mx-space/core/internal/modules/gateway/pageproxy"
	"github.com/mx-space/core/internal/modules/gateway/webhook"
	"github.com/mx-space/core/internal/modules/processing/ai"
	"github.com/mx-space/core/internal/modules/processing/markdown"
	"github.com/mx-space/core/internal/modules/processing/render"
	"github.com/mx-space/core/internal/modules/processing/say"
	"github.com/mx-space/core/internal/modules/serverless/serverless"
	"github.com/mx-space/core/internal/modules/stats/aggregate"
	"github.com/mx-space/core/internal/modules/stats/analyze"
	"github.com/mx-space/core/internal/modules/stats/recently"
	"github.com/mx-space/core/internal/modules/stats/search"
	"github.com/mx-space/core/internal/modules/storage/backup"
	"github.com/mx-space/core/internal/modules/storage/file"
	"github.com/mx-space/core/internal/modules/syndication/feed"
	"github.com/mx-space/core/internal/modules/syndication/reader"
	"github.com/mx-space/core/internal/modules/syndication/sitemap"
	"github.com/mx-space/core/internal/modules/syndication/subscribe"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
	"github.com/mx-space/core/internal/modules/system/core/dependency"
	"github.com/mx-space/core/internal/modules/system/core/health"
	init_ "github.com/mx-space/core/internal/modules/system/core/init"
	"github.com/mx-space/core/internal/modules/system/core/option"
	"github.com/mx-space/core/internal/modules/system/core/update"
	"github.com/mx-space/core/internal/modules/system/util/debug"
	"github.com/mx-space/core/internal/modules/system/util/helper"
	"github.com/mx-space/core/internal/modules/system/util/metapreset"
	"github.com/mx-space/core/internal/modules/system/util/project"
	"github.com/mx-space/core/internal/modules/system/util/pty"
	"github.com/mx-space/core/internal/modules/system/util/servertime"
	"github.com/mx-space/core/internal/modules/system/util/slugtracker"
	"github.com/mx-space/core/internal/modules/tasks/ack"
	"github.com/mx-space/core/internal/modules/tasks/crontask"
	"github.com/mx-space/core/internal/pkg/bark"
	pkgredis "github.com/mx-space/core/internal/pkg/redis"
	"github.com/mx-space/core/internal/pkg/response"
	"github.com/mx-space/core/internal/pkg/taskqueue"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (a *App) registerRoutes(rc *pkgredis.Client) {
	r := a.router
	db := a.db
	authMW := middleware.Auth(db)

	r.NoRoute(func(c *gin.Context) {
		response.NotFound(c)
	})
	r.NoMethod(func(c *gin.Context) {
		response.MethodNotAllowed(c)
	})

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
	render.NewHandler(db, cfgSvc).RegisterRoutes(root, authMW)
	pageproxy.NewHandler(cfgSvc, a.cfg).RegisterRoutes(root)

	// Versioned API
	api := r.Group(apiPrefix)
	api.Use(middleware.OptionalAuth(db))
	api.Use(middleware.HTTPCache(rc.Raw(), middleware.HTTPCacheOptions{
		TTL:                    15 * time.Second,
		EnableCDNHeader:        true,
		EnableForceCacheHeader: false,
		Disable:                a.cfg.IsDev(),
		SkipPaths:              httpCacheSkipPaths(apiPrefix),
	}))

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
		deleted, err := middleware.PurgeHTTPCache(c.Request.Context(), rc.Raw())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":      0,
				"code":    http.StatusInternalServerError,
				"message": err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "deleted": deleted})
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
	subscribe.NewHandler(subscribe.NewService(db), cfgSvc).RegisterRoutes(api, authMW)
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
	file.NewHandler(db, cfgSvc).RegisterRoutes(api, authMW)

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

func httpCacheSkipPaths(apiPrefix string) []string {
	p := strings.TrimSuffix(strings.TrimSpace(apiPrefix), "/")
	if p == "" {
		p = "/api/v2"
	}
	return []string{
		p + "/uptime",
		p + "/like_this",
		p + "/clean_cache",
		p + "/clean_catch",
		p + "/clean_redis",
		p + "/server-time",
		p + "/search",
		p + "/search/",
		p + "/search/type/*",
		p + "/master/allow-login",
		p + "/master/check_logged",
		p + "/user/allow-login",
		p + "/user/check_logged",
		p + "/owner/allow-login",
		p + "/owner/check_logged",
	}
}
