package aggregate

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/configs"
	"github.com/mx-space/core/internal/modules/gateway"
	pkgredis "github.com/mx-space/core/internal/pkg/redis"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

const (
	redisKeyMaxOnlineCount      = "mx:max_online_count"
	redisKeyMaxOnlineCountTotal = "mx:max_online_count:total"

	readLikeTypePost = 0
	readLikeTypeNote = 1
	readLikeTypeAll  = 2
)

type aggregateData struct {
	User         interface{}            `json:"user"`
	SEO          interface{}            `json:"seo"`
	URL          interface{}            `json:"url"`
	Categories   []models.CategoryModel `json:"categories"`
	PageMeta     []pageMeta             `json:"page_meta"`
	LatestNoteID *latestNote            `json:"latest_note_id,omitempty"`
	Theme        interface{}            `json:"theme,omitempty"`
	AI           aggregateAI            `json:"ai"`

	// Legacy fields kept for backward compatibility.
	Tags  []string      `json:"tags,omitempty"`
	Count postNoteCount `json:"count,omitempty"`
}

type postNoteCount struct {
	Posts  int64 `json:"posts"`
	Notes  int64 `json:"notes"`
	Pages  int64 `json:"pages"`
	Topics int64 `json:"topics"`
}

type pageMeta struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Slug  string `json:"slug"`
	Order int    `json:"order"`
}

type latestNote struct {
	ID  string `json:"id"`
	NID int    `json:"nid"`
}

type aggregateAI struct {
	EnableSummary bool `json:"enable_summary"`
}

type userSummary struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Name      string `json:"name"`
	Avatar    string `json:"avatar"`
	Introduce string `json:"introduce"`
	URL       string `json:"url"`
}

type statResponse struct {
	Posts              int64  `json:"posts"`
	Notes              int64  `json:"notes"`
	Pages              int64  `json:"pages"`
	Comments           int64  `json:"comments"`
	Says               int64  `json:"says"`
	Links              int64  `json:"links"`
	Projects           int64  `json:"projects"`
	Snippets           int64  `json:"snippets"`
	Categories         int64  `json:"categories"`
	Topics             int64  `json:"topics"`
	AllComments        int64  `json:"all_comments"`
	UnreadComments     int64  `json:"unread_comments"`
	LinkApply          int64  `json:"link_apply"`
	Recently           int64  `json:"recently"`
	Online             int64  `json:"online"`
	TodayMaxOnline     string `json:"today_max_online"`
	TodayOnlineTotal   string `json:"today_online_total"`
	CallTime           int64  `json:"call_time"`
	UV                 int64  `json:"uv"`
	TodayIPAccessCount int64  `json:"today_ip_access_count"`
}

type readLikeResponse struct {
	Reads      int64 `json:"reads"`
	Likes      int64 `json:"likes"`
	TotalReads int64 `json:"total_reads"`
	TotalLikes int64 `json:"total_likes"`
}

type wordCountResponse struct {
	Words int64 `json:"words"`
	Count int64 `json:"count"`
}

type readLikeTotal struct {
	Reads int64 `gorm:"column:read_total"`
	Likes int64 `gorm:"column:like_total"`
}

type topNote struct {
	ID      string         `json:"id"`
	NID     int            `json:"nid"`
	Title   string         `json:"title"`
	Created time.Time      `json:"created"`
	Images  []models.Image `json:"images"`
}

type topPost struct {
	ID       string         `json:"id"`
	Slug     string         `json:"slug"`
	Title    string         `json:"title"`
	Created  time.Time      `json:"created"`
	Images   []models.Image `json:"images"`
	Category *struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"category"`
}

type timelineNote struct {
	ID       string    `json:"id"`
	NID      int       `json:"nid"`
	Title    string    `json:"title"`
	Weather  string    `json:"weather"`
	Mood     string    `json:"mood"`
	Created  time.Time `json:"created"`
	Modified time.Time `json:"modified"`
	Bookmark bool      `json:"bookmark"`
}

type timelinePost struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Slug     string    `json:"slug"`
	Created  time.Time `json:"created"`
	Modified time.Time `json:"modified"`
	URL      string    `json:"url"`
	Category *struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"category"`
}

type sitemapItem struct {
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"published_at"`
}

func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, cfgSvc *configs.Service, hub *gateway.Hub, rc *pkgredis.Client) {
	rg.GET("/aggregate", func(c *gin.Context) {
		data, err := buildAggregate(db, cfgSvc, c.Query("theme"))
		if err != nil {
			response.InternalError(c, err)
			return
		}
		response.OK(c, data)
	})

	rg.GET("/aggregate/top", middleware.OptionalAuth(db), func(c *gin.Context) {
		size := 6
		if raw := c.Query("size"); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
				size = parsed
			}
		}
		isAdmin := middleware.IsAuthenticated(c)

		postTx := db.Model(&models.PostModel{}).Preload("Category").Order("created_at DESC").Limit(size)
		noteTx := db.Model(&models.NoteModel{}).Order("created_at DESC").Limit(size)
		if !isAdmin {
			postTx = postTx.Where("is_published = ?", true)
			noteTx = noteTx.Where("is_published = ?", true)
		}

		var posts []models.PostModel
		var notes []models.NoteModel
		var says []models.SayModel
		var recentlies []models.RecentlyModel
		if err := postTx.Find(&posts).Error; err != nil {
			response.InternalError(c, err)
			return
		}
		if err := noteTx.Find(&notes).Error; err != nil {
			response.InternalError(c, err)
			return
		}
		if err := db.Order("created_at DESC").Limit(size).Find(&says).Error; err != nil {
			response.InternalError(c, err)
			return
		}
		if err := db.Order("created_at DESC").Limit(size).Find(&recentlies).Error; err != nil {
			response.InternalError(c, err)
			return
		}

		outPosts := make([]topPost, 0, len(posts))
		for _, p := range posts {
			images := p.Images
			if images == nil {
				images = []models.Image{}
			}
			item := topPost{
				ID:      p.ID,
				Slug:    p.Slug,
				Title:   p.Title,
				Created: p.CreatedAt,
				Images:  images,
			}
			if p.Category != nil {
				item.Category = &struct {
					Name string `json:"name"`
					Slug string `json:"slug"`
				}{
					Name: p.Category.Name,
					Slug: p.Category.Slug,
				}
			}
			outPosts = append(outPosts, item)
		}

		outNotes := make([]topNote, 0, len(notes))
		for _, n := range notes {
			images := n.Images
			if images == nil {
				images = []models.Image{}
			}
			outNotes = append(outNotes, topNote{
				ID:      n.ID,
				NID:     n.NID,
				Title:   n.Title,
				Created: n.CreatedAt,
				Images:  images,
			})
		}

		response.OK(c, gin.H{
			"posts":    outPosts,
			"notes":    outNotes,
			"says":     says,
			"recently": recentlies,
		})
	})

	rg.GET("/aggregate/timeline", func(c *gin.Context) {
		sortDir := 1
		if raw := c.Query("sort"); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil {
				sortDir = parsed
			}
		}
		year := 0
		if raw := c.Query("year"); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil {
				year = parsed
			}
		}
		timelineType := -1
		if raw := c.Query("type"); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil {
				timelineType = parsed
			}
		}

		order := "created_at ASC"
		if sortDir < 0 {
			order = "created_at DESC"
		}

		makeYearFilter := func(tx *gorm.DB) *gorm.DB {
			if year <= 0 {
				return tx
			}
			start := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
			end := start.AddDate(1, 0, 0)
			return tx.Where("created_at >= ? AND created_at < ?", start, end)
		}

		data := gin.H{}

		if timelineType == -1 || timelineType == 0 {
			var posts []models.PostModel
			postTx := db.Model(&models.PostModel{}).
				Preload("Category").
				Where("is_published = ?", true).
				Order(order)
			postTx = makeYearFilter(postTx)
			if err := postTx.Find(&posts).Error; err != nil {
				response.InternalError(c, err)
				return
			}
			postOut := make([]timelinePost, 0, len(posts))
			for _, p := range posts {
				item := timelinePost{
					ID:       p.ID,
					Title:    p.Title,
					Slug:     p.Slug,
					Created:  p.CreatedAt,
					Modified: p.UpdatedAt,
				}
				if p.Category != nil {
					item.Category = &struct {
						Name string `json:"name"`
						Slug string `json:"slug"`
					}{
						Name: p.Category.Name,
						Slug: p.Category.Slug,
					}
					item.URL = "/posts/" + p.Category.Slug + "/" + p.Slug
				} else {
					item.URL = "/posts/" + p.Slug
				}
				postOut = append(postOut, item)
			}
			data["posts"] = postOut
		}

		if timelineType == -1 || timelineType == 1 {
			var notes []models.NoteModel
			noteTx := db.Model(&models.NoteModel{}).
				Where("is_published = ?", true).
				Order(order)
			noteTx = makeYearFilter(noteTx)
			if err := noteTx.Find(&notes).Error; err != nil {
				response.InternalError(c, err)
				return
			}
			noteOut := make([]timelineNote, 0, len(notes))
			for _, n := range notes {
				noteOut = append(noteOut, timelineNote{
					ID:       n.ID,
					NID:      n.NID,
					Title:    n.Title,
					Weather:  n.Weather,
					Mood:     n.Mood,
					Created:  n.CreatedAt,
					Modified: n.UpdatedAt,
					Bookmark: n.Bookmark,
				})
			}
			data["notes"] = noteOut
		}

		response.OK(c, gin.H{"data": data})
	})

	rg.GET("/aggregate/sitemap", func(c *gin.Context) {
		baseURL := ""
		if cfg, err := cfgSvc.Get(); err == nil {
			baseURL = strings.TrimRight(cfg.URL.WebURL, "/")
		}

		items := make([]sitemapItem, 0, 64)

		var pages []models.PageModel
		if err := db.Find(&pages).Error; err != nil {
			response.InternalError(c, err)
			return
		}
		for _, p := range pages {
			path := "/" + strings.TrimLeft(p.Slug, "/")
			items = append(items, sitemapItem{
				URL:         baseURL + path,
				PublishedAt: publishedAt(p.CreatedAt, p.UpdatedAt),
			})
		}

		var notes []models.NoteModel
		if err := db.Where("is_published = ?", true).Find(&notes).Error; err != nil {
			response.InternalError(c, err)
			return
		}
		now := time.Now()
		for _, n := range notes {
			if n.PublicAt != nil && n.PublicAt.After(now) {
				continue
			}
			items = append(items, sitemapItem{
				URL:         baseURL + "/notes/" + strconv.Itoa(n.NID),
				PublishedAt: publishedAt(n.CreatedAt, n.UpdatedAt),
			})
		}

		var posts []models.PostModel
		if err := db.Preload("Category").Where("is_published = ?", true).Find(&posts).Error; err != nil {
			response.InternalError(c, err)
			return
		}
		for _, p := range posts {
			categorySlug := "uncategorized"
			if p.Category != nil && p.Category.Slug != "" {
				categorySlug = p.Category.Slug
			}
			items = append(items, sitemapItem{
				URL:         baseURL + "/posts/" + categorySlug + "/" + p.Slug,
				PublishedAt: publishedAt(p.CreatedAt, p.UpdatedAt),
			})
		}

		sort.Slice(items, func(i, j int) bool {
			return items[i].PublishedAt.After(items[j].PublishedAt)
		})
		response.OK(c, gin.H{"data": items})
	})

	rg.GET("/aggregate/feed", func(c *gin.Context) {
		cfg, err := cfgSvc.Get()
		if err != nil {
			response.InternalError(c, err)
			return
		}

		baseURL := strings.TrimRight(cfg.URL.WebURL, "/")
		var user models.UserModel
		_ = db.Select("name").First(&user).Error
		author := user.Name

		type feedItem struct {
			Created  *time.Time     `json:"created"`
			Modified *time.Time     `json:"modified"`
			Link     string         `json:"link"`
			Title    string         `json:"title"`
			Text     string         `json:"text"`
			ID       string         `json:"id"`
			Images   []models.Image `json:"images"`
		}

		feedItems := make([]feedItem, 0, 20)

		var posts []models.PostModel
		if err := db.Preload("Category").Where("is_published = ?", true).Order("created_at DESC").Limit(10).Find(&posts).Error; err != nil {
			response.InternalError(c, err)
			return
		}
		for _, p := range posts {
			categorySlug := "uncategorized"
			if p.Category != nil && p.Category.Slug != "" {
				categorySlug = p.Category.Slug
			}
			created := p.CreatedAt
			modified := p.UpdatedAt
			images := p.Images
			if images == nil {
				images = []models.Image{}
			}
			feedItems = append(feedItems, feedItem{
				Created:  &created,
				Modified: &modified,
				Link:     baseURL + "/posts/" + categorySlug + "/" + p.Slug,
				Title:    p.Title,
				Text:     p.Text,
				ID:       p.ID,
				Images:   images,
			})
		}

		var notes []models.NoteModel
		if err := db.Where("is_published = ?", true).Order("created_at DESC").Limit(10).Find(&notes).Error; err != nil {
			response.InternalError(c, err)
			return
		}
		for _, n := range notes {
			if n.Password != "" {
				continue
			}
			if n.PublicAt != nil && n.PublicAt.After(time.Now()) {
				continue
			}
			created := n.CreatedAt
			modified := n.UpdatedAt
			images := n.Images
			if images == nil {
				images = []models.Image{}
			}
			feedItems = append(feedItems, feedItem{
				Created:  &created,
				Modified: &modified,
				Link:     baseURL + "/notes/" + strconv.Itoa(n.NID),
				Title:    n.Title,
				Text:     n.Text,
				ID:       n.ID,
				Images:   images,
			})
		}

		sort.Slice(feedItems, func(i, j int) bool {
			li := feedItems[i].Created
			lj := feedItems[j].Created
			if li == nil || lj == nil {
				return false
			}
			return li.After(*lj)
		})
		if len(feedItems) > 10 {
			feedItems = feedItems[:10]
		}

		response.OK(c, gin.H{
			"title":       cfg.SEO.Title,
			"description": cfg.SEO.Description,
			"author":      author,
			"url":         cfg.URL.WebURL,
			"data":        feedItems,
		})
	})

	rg.GET("/aggregate/stat", func(c *gin.Context) {
		var stat statResponse
		db.Model(&models.PostModel{}).Count(&stat.Posts)
		db.Model(&models.NoteModel{}).Count(&stat.Notes)
		db.Model(&models.PageModel{}).Count(&stat.Pages)
		db.Model(&models.CommentModel{}).
			Where("parent_id IS NULL").
			Where("state IN ?", []models.CommentState{models.CommentRead, models.CommentUnread}).
			Count(&stat.Comments)
		db.Model(&models.CommentModel{}).
			Where("state IN ?", []models.CommentState{models.CommentRead, models.CommentUnread}).
			Count(&stat.AllComments)
		db.Model(&models.CommentModel{}).Where("state = ?", models.CommentUnread).Count(&stat.UnreadComments)
		db.Model(&models.SayModel{}).Count(&stat.Says)
		db.Model(&models.LinkModel{}).Where("state = ?", models.LinkPass).Count(&stat.Links)
		db.Model(&models.LinkModel{}).Where("state = ?", models.LinkAudit).Count(&stat.LinkApply)
		db.Model(&models.ProjectModel{}).Count(&stat.Projects)
		db.Model(&models.SnippetModel{}).Count(&stat.Snippets)
		db.Model(&models.CategoryModel{}).Count(&stat.Categories)
		db.Model(&models.TopicModel{}).Count(&stat.Topics)
		db.Model(&models.RecentlyModel{}).Count(&stat.Recently)

		if callTime, ok := loadStatCounterFromOptions(db, "apiCallTime", "api_call_time", "call_time"); ok {
			stat.CallTime = callTime
		} else {
			db.Model(&models.AnalyzeModel{}).Count(&stat.CallTime)
		}
		if uv, ok := loadStatCounterFromOptions(db, "uv"); ok {
			stat.UV = uv
		} else {
			db.Model(&models.AnalyzeModel{}).Distinct("ip").Count(&stat.UV)
		}

		todayStart := beginningOfDay(time.Now())
		db.Model(&models.AnalyzeModel{}).Where("timestamp >= ?", todayStart).Distinct("ip").Count(&stat.TodayIPAccessCount)

		stat.TodayMaxOnline = "0"
		stat.TodayOnlineTotal = "0"
		stat.Online = 0
		if hub != nil {
			stat.Online = int64(hub.ClientCount(gateway.RoomPublic))
		}
		if rc != nil {
			dateKey := shortDateKey(time.Now())
			if todayMaxOnline, err := rc.Raw().HGet(c.Request.Context(), redisKeyMaxOnlineCount, dateKey).Result(); err == nil && strings.TrimSpace(todayMaxOnline) != "" {
				stat.TodayMaxOnline = todayMaxOnline
			}
			if todayOnlineTotal, err := rc.Raw().HGet(c.Request.Context(), redisKeyMaxOnlineCountTotal, dateKey).Result(); err == nil && strings.TrimSpace(todayOnlineTotal) != "" {
				stat.TodayOnlineTotal = todayOnlineTotal
			}
		}
		response.OK(c, stat)
	})

	rg.GET("/aggregate/count_read_and_like", func(c *gin.Context) {
		requestType := parseReadLikeType(c.Query("type"))
		legacyCompatible := !isTruthy(c.Query("accurate"))

		postTotals, err := loadReadLikeTotal(db.Model(&models.PostModel{}))
		if err != nil {
			response.InternalError(c, err)
			return
		}
		noteTotals, err := loadReadLikeTotal(db.Model(&models.NoteModel{}))
		if err != nil {
			response.InternalError(c, err)
			return
		}

		counts := buildReadLikeResponse(postTotals, noteTotals, requestType, legacyCompatible)
		response.OK(c, counts)
	})

	rg.GET("/aggregate/count_site_words", func(c *gin.Context) {
		var totalWords int64
		var posts []models.PostModel
		db.Select("text").Find(&posts)
		for _, p := range posts {
			totalWords += int64(utf8.RuneCountInString(p.Text))
		}
		var notes []models.NoteModel
		db.Select("text").Find(&notes)
		for _, n := range notes {
			totalWords += int64(utf8.RuneCountInString(n.Text))
		}
		var pages []models.PageModel
		db.Select("text").Find(&pages)
		for _, pg := range pages {
			totalWords += int64(utf8.RuneCountInString(pg.Text))
		}
		response.OK(c, wordCountResponse{Words: totalWords, Count: totalWords})
	})

	rg.GET("/aggregate/stat/category-distribution", func(c *gin.Context) {
		var categories []models.CategoryModel
		db.Where("type = ?", 0).Order("created_at ASC").Find(&categories)

		type row struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Slug  string `json:"slug"`
			Count int64  `json:"count"`
		}
		out := make([]row, 0, len(categories))
		for _, cat := range categories {
			var count int64
			db.Model(&models.PostModel{}).Where("category_id = ?", cat.ID).Count(&count)
			out = append(out, row{
				ID: cat.ID, Name: cat.Name, Slug: cat.Slug, Count: count,
			})
		}
		response.OK(c, out)
	})

	rg.GET("/aggregate/stat/tag-cloud", func(c *gin.Context) {
		var rows []struct{ Tags string }
		db.Model(&models.PostModel{}).Select("tags").Find(&rows)

		counts := map[string]int64{}
		for _, row := range rows {
			var tags []string
			if err := json.Unmarshal([]byte(row.Tags), &tags); err != nil {
				continue
			}
			for _, t := range tags {
				tag := strings.TrimSpace(t)
				if tag == "" {
					continue
				}
				counts[tag]++
			}
		}

		type tagCount struct {
			Tag   string `json:"tag"`
			Count int64  `json:"count"`
		}
		out := make([]tagCount, 0, len(counts))
		for tag, count := range counts {
			out = append(out, tagCount{Tag: tag, Count: count})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
		if len(out) > 20 {
			out = out[:20]
		}
		response.OK(c, out)
	})

	rg.GET("/aggregate/stat/publication-trend", func(c *gin.Context) {
		start := time.Now().AddDate(0, -11, 0)
		type trend struct {
			Date  string `json:"date"`
			Posts int64  `json:"posts"`
			Notes int64  `json:"notes"`
		}
		out := make([]trend, 0, 12)
		for i := 0; i < 12; i++ {
			monthStart := time.Date(start.Year(), start.Month()+time.Month(i), 1, 0, 0, 0, 0, time.Local)
			monthEnd := monthStart.AddDate(0, 1, 0)
			var postsCount int64
			var notesCount int64
			db.Model(&models.PostModel{}).Where("created_at >= ? AND created_at < ?", monthStart, monthEnd).Count(&postsCount)
			db.Model(&models.NoteModel{}).Where("created_at >= ? AND created_at < ?", monthStart, monthEnd).Count(&notesCount)
			out = append(out, trend{
				Date:  monthStart.Format("2006-01"),
				Posts: postsCount,
				Notes: notesCount,
			})
		}
		response.OK(c, out)
	})

	rg.GET("/aggregate/stat/top-articles", func(c *gin.Context) {
		var posts []models.PostModel
		db.Preload("Category").
			Select("id, title, slug, read_count, like_count, category_id").
			Where("is_published = ?", true).
			Order("read_count DESC").
			Limit(10).
			Find(&posts)

		type article struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Slug     string `json:"slug"`
			Reads    int    `json:"reads"`
			Likes    int    `json:"likes"`
			Category *struct {
				Name string `json:"name"`
				Slug string `json:"slug"`
			} `json:"category"`
		}
		out := make([]article, 0, len(posts))
		for _, p := range posts {
			var cat *struct {
				Name string `json:"name"`
				Slug string `json:"slug"`
			}
			if p.CategoryID != nil && p.Category.ID != "" {
				cat = &struct {
					Name string `json:"name"`
					Slug string `json:"slug"`
				}{Name: p.Category.Name, Slug: p.Category.Slug}
			}
			out = append(out, article{
				ID:       p.ID,
				Title:    p.Title,
				Slug:     p.Slug,
				Reads:    p.ReadCount,
				Likes:    p.LikeCount,
				Category: cat,
			})
		}
		response.OK(c, out)
	})

	rg.GET("/aggregate/stat/comment-activity", func(c *gin.Context) {
		type dayCount struct {
			Date  string `json:"date"`
			Count int64  `json:"count"`
		}
		out := make([]dayCount, 0, 30)
		start := time.Now().AddDate(0, 0, -29)
		for i := 0; i < 30; i++ {
			dayStart := time.Date(start.Year(), start.Month(), start.Day()+i, 0, 0, 0, 0, time.Local)
			dayEnd := dayStart.AddDate(0, 0, 1)
			var count int64
			db.Model(&models.CommentModel{}).
				Where("created_at >= ? AND created_at < ?", dayStart, dayEnd).
				Count(&count)
			out = append(out, dayCount{
				Date:  dayStart.Format("2006-01-02"),
				Count: count,
			})
		}
		response.OK(c, out)
	})

	rg.GET("/aggregate/stat/traffic-source", func(c *gin.Context) {
		cutoff := time.Now().AddDate(0, 0, -7)
		var rows []models.AnalyzeModel
		db.Select("ua").Where("timestamp >= ?", cutoff).Find(&rows)

		osCount := map[string]int64{}
		browserCount := map[string]int64{}
		for _, row := range rows {
			raw, _ := row.UA["raw"].(string)
			os := detectOS(raw)
			browser := detectBrowser(raw)
			osCount[os]++
			browserCount[browser]++
		}

		toList := func(m map[string]int64) []gin.H {
			out := make([]gin.H, 0, len(m))
			for name, count := range m {
				out = append(out, gin.H{"name": name, "count": count})
			}
			sort.Slice(out, func(i, j int) bool { return out[i]["count"].(int64) > out[j]["count"].(int64) })
			return out
		}

		response.OK(c, gin.H{
			"os":      toList(osCount),
			"browser": toList(browserCount),
		})
	})
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

func loadStatCounterFromOptions(db *gorm.DB, names ...string) (int64, bool) {
	type optionValue struct {
		Value string
	}
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		var row optionValue
		if err := db.Model(&models.OptionModel{}).
			Select("value").
			Where("name = ?", trimmed).
			First(&row).Error; err != nil {
			continue
		}
		if v, ok := parseOptionCounter(row.Value); ok {
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

func isTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
