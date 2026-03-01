package aggregate

import (
	"encoding/json"

	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/system/core/configs"
	"gorm.io/gorm"
)

func buildAggregate(db *gorm.DB, cfgSvc *configs.Service, themeName string) (*aggregateData, error) {
	var user models.UserModel
	db.First(&user)

	cfg, err := cfgSvc.Get()
	if err != nil {
		return nil, err
	}

	var categories []models.CategoryModel
	db.Where("type = ?", 0).Order("created_at ASC").Find(&categories)

	var pages []models.PageModel
	db.Select("id, title, slug, order_num, created_at, updated_at").Order("order_num DESC, updated_at DESC").Find(&pages)
	pageMetaList := make([]pageMeta, 0, len(pages))
	for _, p := range pages {
		pageMetaList = append(pageMetaList, pageMeta{
			ID: p.ID, Title: p.Title, Slug: p.Slug, Order: p.Order,
		})
	}

	var latest models.NoteModel
	var latestNoteID *latestNote
	if err := db.Select("id, n_id").Where("is_published = ?", true).Order("created_at DESC").First(&latest).Error; err == nil {
		latestNoteID = &latestNote{ID: latest.ID, NID: latest.NID}
	}

	// Collect unique tags from published posts
	var tagRows []struct{ Tags string }
	db.Model(&models.PostModel{}).
		Where("is_published = ?", true).
		Select("tags").
		Scan(&tagRows)

	tagSet := map[string]struct{}{}
	for _, row := range tagRows {
		var tags []string
		if err := json.Unmarshal([]byte(row.Tags), &tags); err == nil {
			for _, t := range tags {
				if t != "" {
					tagSet[t] = struct{}{}
				}
			}
		}
	}
	tags := make([]string, 0, len(tagSet))
	for t := range tagSet {
		tags = append(tags, t)
	}

	var cnt postNoteCount
	db.Model(&models.PostModel{}).Where("is_published = ?", true).Count(&cnt.Posts)
	db.Model(&models.NoteModel{}).Where("is_published = ?", true).Count(&cnt.Notes)
	db.Model(&models.PageModel{}).Count(&cnt.Pages)
	db.Model(&models.TopicModel{}).Count(&cnt.Topics)

	var theme interface{}
	if themeName != "" {
		var snippet models.SnippetModel
		if err := db.Where("reference = ? AND name = ?", "theme", themeName).First(&snippet).Error; err == nil {
			var parsed interface{}
			if json.Unmarshal([]byte(snippet.Raw), &parsed) == nil {
				theme = parsed
			} else {
				theme = snippet.Raw
			}
		}
	}

	return &aggregateData{
		User: userSummary{
			ID: user.ID, Username: user.Username, Name: user.Name,
			Avatar: user.Avatar, Introduce: user.Introduce, URL: user.URL,
		},
		SEO:          cfg.SEO,
		URL:          cfg.URL,
		Categories:   categories,
		PageMeta:     pageMetaList,
		LatestNoteID: latestNoteID,
		Theme:        theme,
		AI: aggregateAI{
			EnableSummary: cfg.AI.EnableSummary,
		},
		Tags:  tags,
		Count: cnt,
	}, nil
}
