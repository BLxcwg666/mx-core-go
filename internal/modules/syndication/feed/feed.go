package feed

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/system/core/configs"
	"gorm.io/gorm"
)

// RegisterRoutes mounts RSS and Atom feed endpoints.
func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, cfgSvc *configs.Service) {
	rg.GET("/feed", func(c *gin.Context) {
		feedType := c.DefaultQuery("type", "rss") // rss | atom
		renderFeed(c, db, cfgSvc, feedType)
	})
	rg.GET("/feed.xml", func(c *gin.Context) {
		renderFeed(c, db, cfgSvc, "rss")
	})
	rg.GET("/atom.xml", func(c *gin.Context) {
		renderFeed(c, db, cfgSvc, "atom")
	})
}

type feedItem struct {
	Title   string
	Link    string
	GUID    string
	PubDate time.Time
	Content string
}

func renderFeed(c *gin.Context, db *gorm.DB, cfgSvc *configs.Service, feedType string) {
	cfg, err := cfgSvc.Get()
	if err != nil {
		c.String(500, "config error")
		return
	}

	var posts []models.PostModel
	db.Where("is_published = ?", true).
		Order("created_at DESC").
		Limit(20).
		Find(&posts)

	webURL := cfg.URL.WebURL
	siteTitle := cfg.SEO.Title
	siteDesc := cfg.SEO.Description

	items := make([]feedItem, len(posts))
	for i, p := range posts {
		items[i] = feedItem{
			Title:   p.Title,
			Link:    fmt.Sprintf("%s/posts/%s", webURL, p.Slug),
			GUID:    p.ID,
			PubDate: p.CreatedAt,
			Content: p.Text,
		}
	}

	switch feedType {
	case "atom":
		c.Header("Content-Type", "application/atom+xml; charset=utf-8")
		c.String(200, buildAtom(siteTitle, siteDesc, webURL, items))
	default:
		c.Header("Content-Type", "application/rss+xml; charset=utf-8")
		c.String(200, buildRSS(siteTitle, siteDesc, webURL, items))
	}
}

func buildRSS(title, desc, link string, items []feedItem) string {
	now := time.Now().Format(time.RFC1123Z)
	xml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>%s</title>
    <link>%s</link>
    <description>%s</description>
    <lastBuildDate>%s</lastBuildDate>
`, escapeXML(title), escapeXML(link), escapeXML(desc), now)

	for _, item := range items {
		xml += fmt.Sprintf(`    <item>
      <title>%s</title>
      <link>%s</link>
      <guid>%s</guid>
      <pubDate>%s</pubDate>
      <description><![CDATA[%s]]></description>
    </item>
`, escapeXML(item.Title), escapeXML(item.Link), item.GUID,
			item.PubDate.Format(time.RFC1123Z), item.Content)
	}

	xml += `  </channel>
</rss>`
	return xml
}

func buildAtom(title, desc, link string, items []feedItem) string {
	now := time.Now().Format(time.RFC3339)
	xml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>%s</title>
  <subtitle>%s</subtitle>
  <link href="%s"/>
  <updated>%s</updated>
  <id>%s</id>
`, escapeXML(title), escapeXML(desc), escapeXML(link), now, escapeXML(link))

	for _, item := range items {
		xml += fmt.Sprintf(`  <entry>
    <title>%s</title>
    <link href="%s"/>
    <id>%s</id>
    <updated>%s</updated>
    <content type="html"><![CDATA[%s]]></content>
  </entry>
`, escapeXML(item.Title), escapeXML(item.Link), item.GUID,
			item.PubDate.Format(time.RFC3339), item.Content)
	}

	xml += `</feed>`
	return xml
}

// escapeXML replaces XML special characters in attribute/element content.
func escapeXML(s string) string {
	result := ""
	for _, r := range s {
		switch r {
		case '&':
			result += "&amp;"
		case '<':
			result += "&lt;"
		case '>':
			result += "&gt;"
		case '"':
			result += "&quot;"
		default:
			result += string(r)
		}
	}
	return result
}
