package activity

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func compactPost(p models.PostModel) gin.H {
	return gin.H{
		"id":         p.ID,
		"title":      p.Title,
		"slug":       p.Slug,
		"created":    p.CreatedAt,
		"modified":   p.UpdatedAt,
		"categoryId": p.CategoryID,
		"category": gin.H{
			"id":      p.Category.ID,
			"name":    p.Category.Name,
			"slug":    p.Category.Slug,
			"type":    p.Category.Type,
			"created": p.Category.CreatedAt,
		},
	}
}

func compactNote(n models.NoteModel) gin.H {
	return gin.H{
		"id":       n.ID,
		"title":    n.Title,
		"nid":      n.NID,
		"created":  n.CreatedAt,
		"modified": n.UpdatedAt,
		"mood":     n.Mood,
		"weather":  n.Weather,
		"bookmark": n.Bookmark,
	}
}

func compactPage(p models.PageModel) gin.H {
	return gin.H{
		"id":      p.ID,
		"title":   p.Title,
		"slug":    p.Slug,
		"created": p.CreatedAt,
	}
}

func compactRecently(r models.RecentlyModel) gin.H {
	return gin.H{
		"id":      r.ID,
		"content": r.Content,
		"up":      r.UpCount,
		"down":    r.DownCount,
		"created": r.CreatedAt,
	}
}

func extractRefIDFromRoomName(roomName string) string {
	if roomName == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(roomName, "article_"):
		return strings.TrimPrefix(roomName, "article_")
	case strings.HasPrefix(roomName, "article:"):
		return strings.TrimPrefix(roomName, "article:")
	default:
		return ""
	}
}

func copyPayload(payload map[string]interface{}) map[string]interface{} {
	if payload == nil {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(payload))
	for k, v := range payload {
		out[k] = v
	}
	return out
}

func strFromAny(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return ""
	}
}

func uniq(values []string) []string {
	if len(values) == 0 {
		return values
	}
	set := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := set[v]; ok {
			continue
		}
		set[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func nowMillis() int64 { return time.Now().UnixMilli() }

func parseMsOrDefault(raw string, def time.Time) time.Time {
	if raw == "" {
		return def
	}
	if ms, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.UnixMilli(ms)
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts
	}
	return def
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func ensureLikeCounterOption(db *gorm.DB) {
	_ = db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoNothing: true,
	}).Create(&models.OptionModel{
		Name:  "like",
		Value: "0",
	}).Error
}
