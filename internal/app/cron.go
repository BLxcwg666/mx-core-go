package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/content/link"
	"github.com/mx-space/core/internal/modules/content/search"
	"github.com/mx-space/core/internal/modules/stats/aggregate"
	"github.com/mx-space/core/internal/modules/storage/backup"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
	pkgcron "github.com/mx-space/core/internal/pkg/cron"
	"gorm.io/gorm"
)

// registerCronJobs registers all scheduled background jobs.
func registerCronJobs(sched *pkgcron.Scheduler, db *gorm.DB, runtimeCfg *config.AppConfig) {
	cfgSvc := appconfigs.NewService(db)
	searchSvc := search.NewService(db, cfgSvc, runtimeCfg)

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

	sched.Register(pkgcron.Job{
		Name:        "sync_meilisearch_index",
		Description: "全量推送搜索索引到 MeiliSearch",
		Interval:    24 * time.Hour,
		Fn: func(ctx context.Context) error {
			cfg, err := cfgSvc.Get()
			if err != nil {
				return err
			}
			enable := cfg.MeiliSearchOptions.Enable
			if runtimeCfg != nil && runtimeCfg.MeiliSearch.HasEnable {
				enable = runtimeCfg.MeiliSearch.Enable
			}
			if !enable {
				return nil
			}
			return searchSvc.IndexAll()
		},
	})

	sched.Register(pkgcron.Job{
		Name:        "push_baidu_search",
		Description: "推送站点 URL 到百度搜索",
		Interval:    24 * time.Hour,
		Fn: func(ctx context.Context) error {
			cfg, err := cfgSvc.Get()
			if err != nil {
				return err
			}
			if !cfg.BaiduSearchOptions.Enable || cfg.BaiduSearchOptions.Token == nil || *cfg.BaiduSearchOptions.Token == "" {
				return nil
			}
			urls, err := aggregate.GetSitemapURLs(db, cfgSvc)
			if err != nil {
				return err
			}
			if len(urls) == 0 {
				return nil
			}
			webURL := strings.TrimRight(cfg.URL.WebURL, "/")
			apiURL := fmt.Sprintf("http://data.zz.baidu.com/urls?site=%s&token=%s", webURL, *cfg.BaiduSearchOptions.Token)
			body := strings.Join(urls, "\n")
			req, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(body))
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "text/plain")
			client := &http.Client{Timeout: 30 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			resp.Body.Close()
			return nil
		},
	})

	sched.Register(pkgcron.Job{
		Name:        "push_bing_search",
		Description: "推送站点 URL 到 Bing 搜索",
		Interval:    24 * time.Hour,
		Fn: func(ctx context.Context) error {
			cfg, err := cfgSvc.Get()
			if err != nil {
				return err
			}
			if !cfg.BingSearchOptions.Enable || cfg.BingSearchOptions.Token == nil || *cfg.BingSearchOptions.Token == "" {
				return nil
			}
			urls, err := aggregate.GetSitemapURLs(db, cfgSvc)
			if err != nil {
				return err
			}
			if len(urls) == 0 {
				return nil
			}
			webURL := strings.TrimRight(cfg.URL.WebURL, "/")
			payload, _ := json.Marshal(map[string]interface{}{
				"siteUrl": webURL,
				"urlList": urls,
			})
			apiURL := fmt.Sprintf("https://ssl.bing.com/webmaster/api.svc/json/SubmitUrlbatch?apikey=%s", *cfg.BingSearchOptions.Token)
			req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(payload))
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "application/json")
			client := &http.Client{Timeout: 30 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			resp.Body.Close()
			return nil
		},
	})
}
