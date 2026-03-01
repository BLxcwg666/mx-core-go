package app

import (
	"context"
	"time"

	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/content/link"
	"github.com/mx-space/core/internal/modules/stats/search"
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
}
