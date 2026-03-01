package markdown

import (
	"time"

	"github.com/mx-space/core/internal/models"
)

// articleSnapshot is a unified view of a post, note, or page used for rendering.
type articleSnapshot struct {
	ID        string
	Title     string
	Text      string
	Slug      string
	NID       int
	CreatedAt time.Time
	UpdatedAt time.Time
	Type      string
	Category  *models.CategoryModel
}

// importDTO is the request body for POST /markdown/import.
type importDTO struct {
	Type string       `json:"type" binding:"required"`
	Data []importItem `json:"data" binding:"required"`
}

type importItem struct {
	Meta *importMeta `json:"meta"`
	Text string      `json:"text" binding:"required"`
}

type importMeta struct {
	Title      string   `json:"title"`
	Date       string   `json:"date"`
	Updated    string   `json:"updated"`
	Categories []string `json:"categories"`
	Tags       []string `json:"tags"`
	Slug       string   `json:"slug"`
}
