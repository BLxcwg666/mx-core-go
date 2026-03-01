package search

import (
	"net/http"
	"time"

	"github.com/mx-space/core/internal/models"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

const (
	servedByMeili = "meilisearch"
	servedByMySQL = "mysql"
)

// SearchResult is a single search hit returned to the client.
type SearchResult struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
	Type    string `json:"type"` // post | note | page
	Slug    string `json:"slug,omitempty"`
	NID     int    `json:"nid,omitempty"`

	Created  *time.Time `json:"created,omitempty"`
	Modified *time.Time `json:"modified,omitempty"`

	CategoryID  *string        `json:"categoryId,omitempty"`
	Category    interface{}    `json:"category,omitempty"`
	Copyright   *bool          `json:"copyright,omitempty"`
	IsPublished *bool          `json:"isPublished,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Count       *models.Count  `json:"count,omitempty"`
	Pin         *bool          `json:"pin,omitempty"`
	PinOrder    *int           `json:"pinOrder,omitempty"`
	Images      []models.Image `json:"images,omitempty"`

	Mood        string           `json:"mood,omitempty"`
	Weather     string           `json:"weather,omitempty"`
	PublicAt    *time.Time       `json:"publicAt,omitempty"`
	Bookmark    *bool            `json:"bookmark,omitempty"`
	Coordinates *models.GeoPoint `json:"coordinates,omitempty"`
	Location    string           `json:"location,omitempty"`
	TopicID     *string          `json:"topicId,omitempty"`
	Topic       interface{}      `json:"topic,omitempty"`

	Subtitle     string                 `json:"subtitle,omitempty"`
	Order        *int                   `json:"order,omitempty"`
	AllowComment *bool                  `json:"allowComment,omitempty"`
	Meta         map[string]interface{} `json:"meta,omitempty"`
}
