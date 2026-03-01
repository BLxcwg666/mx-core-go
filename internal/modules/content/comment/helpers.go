package comment

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
)

func normalizeRefType(raw string) models.RefType {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "post", "posts":
		return models.RefTypePost
	case "note", "notes":
		return models.RefTypeNote
	case "page", "pages":
		return models.RefTypePage
	case "recently", "recentlies":
		return models.RefTypeRecently
	default:
		return models.RefType(v)
	}
}

func refTypeForResponse(rt models.RefType) string {
	switch normalizeRefType(string(rt)) {
	case models.RefTypePost:
		return "posts"
	case models.RefTypeNote:
		return "notes"
	case models.RefTypePage:
		return "pages"
	case models.RefTypeRecently:
		return "recentlies"
	default:
		return string(rt)
	}
}

func refMapKey(refType models.RefType, refID string) string {
	return string(normalizeRefType(string(refType))) + ":" + strings.TrimSpace(refID)
}

func parentLookupKey(refType models.RefType, refID, key string) string {
	return refMapKey(refType, refID) + "|" + strings.TrimSpace(key)
}

func parentKeyFromCommentKey(raw string) string {
	key := strings.TrimSpace(raw)
	if key == "" {
		return ""
	}
	idx := strings.LastIndex(key, "#")
	if idx <= 0 {
		return ""
	}
	return key[:idx]
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	set := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		if _, ok := set[trimmed]; ok {
			continue
		}
		set[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func compactPostRef(post models.PostModel) gin.H {
	item := gin.H{
		"id":          post.ID,
		"title":       post.Title,
		"slug":        post.Slug,
		"category_id": post.CategoryID,
	}
	if post.Category != nil {
		item["category"] = gin.H{
			"id":      post.Category.ID,
			"name":    post.Category.Name,
			"slug":    post.Category.Slug,
			"type":    post.Category.Type,
			"created": post.Category.CreatedAt,
		}
	}
	return item
}

func compactNoteRef(note models.NoteModel) gin.H {
	return gin.H{
		"id":    note.ID,
		"title": note.Title,
		"nid":   note.NID,
	}
}

func compactPageRef(page models.PageModel) gin.H {
	return gin.H{
		"id":    page.ID,
		"title": page.Title,
		"slug":  page.Slug,
	}
}

func compactRecentlyRef(recently models.RecentlyModel) gin.H {
	return gin.H{
		"id":      recently.ID,
		"content": recently.Content,
	}
}
