package aggregate

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/mx-space/core/internal/models"
	"gorm.io/gorm"
)

func publishedAt(created, modified time.Time) time.Time {
	if modified.IsZero() {
		return created
	}
	if modified.After(created) {
		return modified
	}
	return created
}

func beginningOfDay(t time.Time) time.Time {
	local := t.In(time.Local)
	y, m, d := local.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

func shortDateKey(t time.Time) string {
	return t.Format("1-2-06")
}

func isTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func parseReadLikeType(raw string) int {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return readLikeTypeAll
	}
	if parsed, err := strconv.Atoi(trimmed); err == nil {
		switch parsed {
		case readLikeTypePost, readLikeTypeNote, readLikeTypeAll:
			return parsed
		}
	}

	switch strings.ToLower(trimmed) {
	case "post", "posts":
		return readLikeTypePost
	case "note", "notes":
		return readLikeTypeNote
	default:
		return readLikeTypeAll
	}
}

func loadStatCounterFromOptions(db *gorm.DB, names ...string) (int64, bool) {
	type optionValue struct {
		Name  string
		Value string
	}
	if db == nil || len(names) == 0 {
		return 0, false
	}

	orderedNames := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		orderedNames = append(orderedNames, trimmed)
	}
	if len(orderedNames) == 0 {
		return 0, false
	}

	var rows []optionValue
	if err := db.Model(&models.OptionModel{}).
		Select("name, value").
		Where("name IN ?", orderedNames).
		Find(&rows).Error; err != nil {
		return 0, false
	}

	valuesByName := make(map[string]string, len(rows))
	for _, row := range rows {
		valuesByName[strings.TrimSpace(row.Name)] = row.Value
	}
	for _, name := range orderedNames {
		if v, ok := parseOptionCounter(valuesByName[name]); ok {
			return v, true
		}
	}
	return 0, false
}

func parseOptionCounter(raw string) (int64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	var asAny interface{}
	if err := json.Unmarshal([]byte(s), &asAny); err == nil {
		switch v := asAny.(type) {
		case float64:
			return int64(v), true
		case string:
			if i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				return i, true
			}
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				return int64(f), true
			}
		}
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i, true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f), true
	}
	return 0, false
}

func loadReadLikeTotal(tx *gorm.DB) (readLikeTotal, error) {
	var total readLikeTotal
	if tx == nil {
		return total, nil
	}
	err := tx.Select("COALESCE(SUM(read_count), 0) AS read_total, COALESCE(SUM(like_count), 0) AS like_total").Scan(&total).Error
	return total, err
}

func buildReadLikeResponse(postTotals, noteTotals readLikeTotal, requestType int, legacyCompatible bool) readLikeResponse {
	total := readLikeTotal{}
	switch requestType {
	case readLikeTypePost:
		total = postTotals
	case readLikeTypeNote:
		if legacyCompatible {
			// Keep legacy mx-core behavior: note type aggregates posts.
			total = postTotals
		} else {
			total = noteTotals
		}
	default:
		if legacyCompatible {
			// Keep legacy mx-core behavior: all = post + note(type) = post * 2.
			total = readLikeTotal{
				Reads: postTotals.Reads + postTotals.Reads,
				Likes: postTotals.Likes + postTotals.Likes,
			}
		} else {
			total = readLikeTotal{
				Reads: postTotals.Reads + noteTotals.Reads,
				Likes: postTotals.Likes + noteTotals.Likes,
			}
		}
	}

	return readLikeResponse{
		Reads:      total.Reads,
		Likes:      total.Likes,
		TotalReads: total.Reads,
		TotalLikes: total.Likes,
	}
}

func detectOS(ua string) string {
	lower := strings.ToLower(ua)
	switch {
	case strings.Contains(lower, "windows"):
		return "Windows"
	case strings.Contains(lower, "mac os") || strings.Contains(lower, "macintosh"):
		return "macOS"
	case strings.Contains(lower, "android"):
		return "Android"
	case strings.Contains(lower, "iphone") || strings.Contains(lower, "ipad") || strings.Contains(lower, "ios"):
		return "iOS"
	case strings.Contains(lower, "linux"):
		return "Linux"
	default:
		return "Unknown"
	}
}

func detectBrowser(ua string) string {
	lower := strings.ToLower(ua)
	switch {
	case strings.Contains(lower, "edg/"):
		return "Edge"
	case strings.Contains(lower, "chrome/"):
		return "Chrome"
	case strings.Contains(lower, "safari/") && !strings.Contains(lower, "chrome/"):
		return "Safari"
	case strings.Contains(lower, "firefox/"):
		return "Firefox"
	case strings.Contains(lower, "micromessenger"):
		return "WeChat"
	default:
		return "Unknown"
	}
}

type aggregateTotalRow struct {
	Total int64 `gorm:"column:total"`
}

type aggregateBucketCount struct {
	Bucket string `gorm:"column:bucket"`
	Count  int64  `gorm:"column:count"`
}

func loadTextLengthTotal(tx *gorm.DB, column string) (int64, error) {
	if tx == nil {
		return 0, nil
	}
	expr := aggregateTextLengthExpr(tx, column)
	var row aggregateTotalRow
	if err := tx.Select("COALESCE(SUM(" + expr + "), 0) AS total").Scan(&row).Error; err != nil {
		return 0, err
	}
	return row.Total, nil
}

func loadBucketCounts(tx *gorm.DB, timeColumn string, start, end time.Time, granularity string) (map[string]int64, error) {
	counts := map[string]int64{}
	if tx == nil {
		return counts, nil
	}
	expr := aggregateTimeBucketExpr(tx, timeColumn, granularity)
	var rows []aggregateBucketCount
	if err := tx.
		Where(timeColumn+" >= ? AND "+timeColumn+" < ?", start, end).
		Select(expr + " AS bucket, COUNT(*) AS count").
		Group(expr).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts = make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.Bucket] = row.Count
	}
	return counts, nil
}

func aggregateTextLengthExpr(db *gorm.DB, column string) string {
	column = strings.TrimSpace(column)
	if column == "" {
		column = "text"
	}
	switch strings.ToLower(strings.TrimSpace(db.Dialector.Name())) {
	case "sqlite", "sqlite3":
		return "LENGTH(COALESCE(" + column + ", ''))"
	default:
		return "CHAR_LENGTH(COALESCE(" + column + ", ''))"
	}
}

func aggregateTimeBucketExpr(db *gorm.DB, column, granularity string) string {
	column = strings.TrimSpace(column)
	if column == "" {
		column = "created_at"
	}
	granularity = strings.ToLower(strings.TrimSpace(granularity))
	switch strings.ToLower(strings.TrimSpace(db.Dialector.Name())) {
	case "postgres":
		switch granularity {
		case "day":
			return "TO_CHAR(" + column + ", 'YYYY-MM-DD')"
		default:
			return "TO_CHAR(" + column + ", 'YYYY-MM')"
		}
	case "sqlite", "sqlite3":
		switch granularity {
		case "day":
			return "strftime('%Y-%m-%d', " + column + ")"
		default:
			return "strftime('%Y-%m', " + column + ")"
		}
	default:
		switch granularity {
		case "day":
			return "DATE_FORMAT(" + column + ", '%Y-%m-%d')"
		default:
			return "DATE_FORMAT(" + column + ", '%Y-%m')"
		}
	}
}
