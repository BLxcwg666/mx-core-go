package analyze

import (
	"context"
	"strconv"
	"time"

	"github.com/mx-space/core/internal/models"
	pkgredis "github.com/mx-space/core/internal/pkg/redis"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The dashboard totals ("API 总调用次数" and UV) are kept in the options table like the original core,
// because the analyzes table only holds the last 90 days. Migrated sites already have these rows;
// on a fresh site they are seeded from the analyzes table on first use.

func recordCounters(db *gorm.DB, ip string) {
	incrementCounter(db, "apiCallTime", func() int64 {
		var n int64
		db.Model(&models.AnalyzeModel{}).Count(&n)
		return n
	})
	if firstVisitToday(ip) {
		incrementCounter(db, "uv", func() int64 {
			var n int64
			db.Model(&models.AnalyzeModel{}).Distinct("ip").Count(&n)
			return n
		})
	}
}

func incrementCounter(db *gorm.DB, name string, seed func() int64) {
	result := db.Model(&models.OptionModel{}).
		Where("name = ?", name).
		Update("value", gorm.Expr("CAST(value AS UNSIGNED) + 1"))
	if result.Error != nil || result.RowsAffected > 0 {
		return
	}
	db.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&models.OptionModel{Name: name, Value: strconv.FormatInt(seed(), 10)})
}

// firstVisitToday reports whether ip has not been counted as a visitor today (needs Redis).
func firstVisitToday(ip string) bool {
	rc := pkgredis.Default
	if rc == nil || ip == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	key := "mx:analyze:uv:" + time.Now().Format("2006-01-02") + ":" + ip
	set, err := rc.Raw().SetNX(ctx, key, 1, 25*time.Hour).Result()
	return err == nil && set
}
