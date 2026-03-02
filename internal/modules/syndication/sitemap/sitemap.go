package sitemap

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/system/core/configs"
	"gorm.io/gorm"
)

func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, cfgSvc *configs.Service) {
	render := func(c *gin.Context) {
		xml, err := buildSitemap(db, cfgSvc)
		if err != nil {
			c.String(500, "error generating sitemap")
			return
		}
		c.Header("Content-Type", "application/xml; charset=utf-8")
		c.String(200, xml)
	}
	rg.GET("/sitemap.xml", render)
	rg.GET("/sitemap", render)
}

type sitemapURL struct {
	Loc        string
	LastMod    time.Time
	ChangeFreq string
	Priority   float64
}

func buildSitemap(db *gorm.DB, cfgSvc *configs.Service) (string, error) {
	cfg, err := cfgSvc.Get()
	if err != nil {
		return "", err
	}
	base := cfg.URL.WebURL

	var urls []sitemapURL

	urls = append(urls, sitemapURL{
		Loc: base, LastMod: time.Now(),
		ChangeFreq: "daily", Priority: 1.0,
	})

	var posts []models.PostModel
	db.Where("is_published = ?", true).Select("slug, updated_at").Find(&posts)
	for _, p := range posts {
		urls = append(urls, sitemapURL{
			Loc:        fmt.Sprintf("%s/posts/%s", base, p.Slug),
			LastMod:    p.UpdatedAt,
			ChangeFreq: "weekly",
			Priority:   0.8,
		})
	}

	var notes []models.NoteModel
	db.Where("is_published = ?", true).Select("n_id, updated_at").Find(&notes)
	for _, n := range notes {
		urls = append(urls, sitemapURL{
			Loc:        fmt.Sprintf("%s/notes/%d", base, n.NID),
			LastMod:    n.UpdatedAt,
			ChangeFreq: "monthly",
			Priority:   0.6,
		})
	}

	var pages []models.PageModel
	db.Select("slug, updated_at").Find(&pages)
	for _, pg := range pages {
		urls = append(urls, sitemapURL{
			Loc:        fmt.Sprintf("%s/%s", base, pg.Slug),
			LastMod:    pg.UpdatedAt,
			ChangeFreq: "monthly",
			Priority:   0.5,
		})
	}

	return renderXML(urls), nil
}

func renderXML(urls []sitemapURL) string {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
`
	for _, u := range urls {
		xml += fmt.Sprintf(`  <url>
    <loc>%s</loc>
    <lastmod>%s</lastmod>
    <changefreq>%s</changefreq>
    <priority>%.1f</priority>
  </url>
`, escapeXML(u.Loc), u.LastMod.Format("2006-01-02"), u.ChangeFreq, u.Priority)
	}
	xml += `</urlset>`
	return xml
}

func escapeXML(s string) string {
	out := ""
	for _, r := range s {
		switch r {
		case '&':
			out += "&amp;"
		case '<':
			out += "&lt;"
		case '>':
			out += "&gt;"
		default:
			out += string(r)
		}
	}
	return out
}
