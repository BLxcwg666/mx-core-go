package aggregate

import (
	"time"

	"github.com/mx-space/core/internal/models"
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
