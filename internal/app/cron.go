package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/content/link"
	"github.com/mx-space/core/internal/modules/content/search"
	"github.com/mx-space/core/internal/modules/gateway/webhook"
	"github.com/mx-space/core/internal/modules/stats/aggregate"
	"github.com/mx-space/core/internal/modules/storage/backup"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
	pkgcron "github.com/mx-space/core/internal/pkg/cron"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// registerCronJobs registers all scheduled background jobs.
func registerCronJobs(sched *pkgcron.Scheduler, db *gorm.DB, runtimeCfg *config.AppConfig, logger *zap.Logger) {
	cfgSvc := appconfigs.NewService(db, appconfigs.WithLogger(logger))
	searchSvc := search.NewService(db, cfgSvc, runtimeCfg, search.WithLogger(logger))
	cronLogger := logger.Named("CronService")

	sched.Register(pkgcron.Job{
		Name:        "cleanup_analytics",
		Description: "清理 90 天以上的访问记录",
		Interval:    24 * time.Hour,
		Fn: func(ctx context.Context) error {
			cutoff := time.Now().AddDate(0, 0, -90)
			result := db.Where("created_at < ?", cutoff).Delete(&models.AnalyzeModel{})
			if result.Error != nil {
				cronLogger.Warn("清理访问记录失败", zap.Error(result.Error))
				return result.Error
			}
			cronLogger.Info(fmt.Sprintf("清理访问记录成功，共删除 %d 条", result.RowsAffected))
			return nil
		},
	})

	sched.Register(pkgcron.Job{
		Name:        "check_links",
		Description: "检查友链可用性",
		Interval:    12 * time.Hour,
		Offset:      2 * time.Hour,
		Fn: func(ctx context.Context) error {
			svc := link.NewServiceWithLogger(db, logger)
			results := svc.HealthCheck(models.LinkPass, models.LinkOutdate)
			outdated := 0
			recovered := 0
			stateChanged := false
			for _, r := range results {
				if r.Status == 0 || (r.Status >= http.StatusBadRequest && r.Status != http.StatusForbidden) {
					updateResult := db.Model(&models.LinkModel{}).
						Where("id = ? AND state = ?", r.ID, models.LinkPass).
						Update("state", models.LinkOutdate)
					if updateResult.RowsAffected > 0 {
						stateChanged = true
					}
					outdated++
					continue
				}

				updateResult := db.Model(&models.LinkModel{}).
					Where("id = ? AND state = ?", r.ID, models.LinkOutdate).
					Update("state", models.LinkPass)
				if updateResult.RowsAffected > 0 {
					recovered++
					stateChanged = true
				}
			}
			if stateChanged {
				webhook.NewService(db).DispatchScoped(
					"CONTENT_REFRESH",
					map[string]interface{}{"type": "link"},
					webhook.ScopeToSystem|webhook.ScopeToVisitor,
				)
			}
			cronLogger.Info(fmt.Sprintf("友链检查完成，共 %d 个，%d 个不可用，%d 个已恢复", len(results), outdated, recovered))
			return nil
		},
	})

	sched.Register(pkgcron.Job{
		Name:        "auto_backup",
		Description: "自动备份数据库和上传文件到本地，开启上传 S3 时同步上传",
		Interval:    24 * time.Hour,
		Offset:      time.Hour,
		Fn: func(ctx context.Context) error {
			cfg, err := cfgSvc.Get()
			if err != nil {
				return err
			}
			if cfg == nil || !cfg.BackupOptions.AutoBackup {
				cronLogger.Info("自动备份已关闭，跳过")
				return nil
			}
			_, err = backup.CreateBackup(ctx, db, cfgSvc, cronLogger)
			return err
		},
	})

	sched.Register(pkgcron.Job{
		Name:        "sync_meilisearch_index",
		Description: "全量推送搜索索引到 MeiliSearch",
		Interval:    24 * time.Hour,
		Offset:      3 * time.Hour,
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
			cronLogger.Info("全量推送搜索索引到 MeiliSearch...")
			if err := searchSvc.IndexAll(); err != nil {
				cronLogger.Warn("MeiliSearch 索引推送失败", zap.Error(err))
				return err
			}
			cronLogger.Info("MeiliSearch 索引推送完成")
			return nil
		},
	})

	sched.Register(pkgcron.Job{
		Name:        "push_baidu_search",
		Description: "推送站点 URL 到百度搜索",
		Interval:    24 * time.Hour,
		Offset:      13 * time.Hour,
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
			cronLogger.Info(fmt.Sprintf("推送 %d 条 URL 到百度搜索...", len(urls)))
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
				cronLogger.Warn("百度搜索推送失败", zap.Error(err))
				return err
			}
			if err := checkPushResponse(resp); err != nil {
				cronLogger.Warn("百度搜索推送失败", zap.Error(err))
				return err
			}
			cronLogger.Info("百度搜索推送完成")
			return nil
		},
	})

	sched.Register(pkgcron.Job{
		Name:        "push_bing_search",
		Description: "推送站点 URL 到 Bing 搜索",
		Interval:    24 * time.Hour,
		Offset:      13*time.Hour + 10*time.Minute,
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
			cronLogger.Info(fmt.Sprintf("推送 %d 条 URL 到 Bing 搜索...", len(urls)))
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
				cronLogger.Warn("Bing 搜索推送失败", zap.Error(err))
				return err
			}
			if err := checkPushResponse(resp); err != nil {
				cronLogger.Warn("Bing 搜索推送失败", zap.Error(err))
				return err
			}
			cronLogger.Info("Bing 搜索推送完成")
			return nil
		},
	})
}

// checkPushResponse turns a non-2xx answer of a search engine push API into an error,
// so the cron task shows as failed instead of fulfilled.
func checkPushResponse(resp *http.Response) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}
