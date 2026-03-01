package post

import (
	"time"

	"github.com/mx-space/core/internal/models"
)

// CreatePostDTO is the request body for creating a post.
type CreatePostDTO struct {
	Slug         string         `json:"slug"         binding:"required"`
	Title        string         `json:"title"        binding:"required"`
	Text         string         `json:"text"         binding:"required"`
	Summary      string         `json:"summary"`
	CategoryID   *string        `json:"categoryId"`
	Copyright    *bool          `json:"copyright"`
	IsPublished  *bool          `json:"isPublished"`
	AllowComment *bool          `json:"allowComment"`
	Tags         []string       `json:"tags"`
	Pin          *bool          `json:"pin"`
	PinOrder     *int           `json:"pinOrder"`
	Images       []models.Image `json:"images"`
}

// UpdatePostDTO is the request body for updating a post (all fields optional).
type UpdatePostDTO struct {
	Slug         *string        `json:"slug"`
	Title        *string        `json:"title"`
	Text         *string        `json:"text"`
	Summary      *string        `json:"summary"`
	CategoryID   *string        `json:"categoryId"`
	Copyright    *bool          `json:"copyright"`
	IsPublished  *bool          `json:"isPublished"`
	AllowComment *bool          `json:"allowComment"`
	Tags         []string       `json:"tags"`
	Pin          *bool          `json:"pin"`
	PinOrder     *int           `json:"pinOrder"`
	Images       []models.Image `json:"images"`
}

// ListQuery holds query params for listing posts.
type ListQuery struct {
	Year     *int    `form:"year"`
	Category *string `form:"category"`
	Tag      *string `form:"tag"`
	Truncate *bool   `form:"truncate"`
}

// postResponse is the API response shape for a post.
type postResponse struct {
	ID           string         `json:"id"`
	Slug         string         `json:"slug"`
	Title        string         `json:"title"`
	Text         string         `json:"text"`
	Summary      string         `json:"summary"`
	CategoryID   *string        `json:"categoryId"`
	Category     interface{}    `json:"category"`
	Copyright    bool           `json:"copyright"`
	IsPublished  bool           `json:"isPublished"`
	AllowComment bool           `json:"allowComment"`
	Tags         []string       `json:"tags"`
	Count        models.Count   `json:"count"`
	Pin          bool           `json:"pin"`
	PinOrder     int            `json:"pinOrder"`
	Images       []models.Image `json:"images"`
	Created      time.Time      `json:"created"`
	Modified     *time.Time     `json:"modified"`
}

func toResponse(p *models.PostModel) postResponse {
	tags := p.Tags
	if tags == nil {
		tags = []string{}
	}
	images := p.Images
	if images == nil {
		images = []models.Image{}
	}
	var modified *time.Time
	if !p.UpdatedAt.IsZero() {
		modifiedAt := p.UpdatedAt
		modified = &modifiedAt
	}
	return postResponse{
		ID:           p.ID,
		Slug:         p.Slug,
		Title:        p.Title,
		Text:         p.Text,
		Summary:      p.Summary,
		CategoryID:   p.CategoryID,
		Category:     p.Category,
		Copyright:    p.Copyright,
		IsPublished:  p.IsPublished,
		AllowComment: p.AllowComment,
		Tags:         tags,
		Count:        p.GetCount(),
		Pin:          p.Pin,
		PinOrder:     p.PinOrder,
		Images:       images,
		Created:      p.CreatedAt,
		Modified:     modified,
	}
}
