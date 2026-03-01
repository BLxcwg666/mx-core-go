package aggregate

import (
	"fmt"
	"strings"
	"time"

	"github.com/mx-space/core/internal/models"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
	"gorm.io/gorm"
)

// GetSitemapURLs returns all public-facing URLs suitable for search engine
// submission. It covers published posts, published notes, and all pages.
func GetSitemapURLs(db *gorm.DB, cfgSvc *appconfigs.Service) ([]string, error) {
	cfg, err := cfgSvc.Get()
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(cfg.URL.WebURL, "/")
	if base == "" {
		return nil, fmt.Errorf("web_url is not configured")
	}

	var urls []string

	// Homepage
	urls = append(urls, base)

	// Posts
	var posts []models.PostModel
	if err := db.Where("is_published = ?", true).Preload("Category").Select("id, slug, category_id").Find(&posts).Error; err != nil {
		return nil, err
	}
	for _, p := range posts {
		categorySlug := "uncategorized"
		if p.Category != nil && strings.TrimSpace(p.Category.Slug) != "" {
			categorySlug = strings.TrimSpace(p.Category.Slug)
		}
		urls = append(urls, fmt.Sprintf("%s/posts/%s/%s", base, categorySlug, p.Slug))
	}

	// Notes
	var notes []models.NoteModel
	now := time.Now()
	if err := db.Where("is_published = ? AND (public_at IS NULL OR public_at <= ?)", true, now).Select("n_id").Find(&notes).Error; err != nil {
		return nil, err
	}
	for _, n := range notes {
		urls = append(urls, fmt.Sprintf("%s/notes/%d", base, n.NID))
	}

	// Pages
	var pages []models.PageModel
	if err := db.Select("slug").Find(&pages).Error; err != nil {
		return nil, err
	}
	for _, pg := range pages {
		urls = append(urls, fmt.Sprintf("%s/%s", base, pg.Slug))
	}

	return urls, nil
}
