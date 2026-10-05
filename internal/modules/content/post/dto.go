package post

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/nullable"
)

// CreatePostDTO is the request body for creating a post.
type CreatePostDTO struct {
	Slug         string                 `json:"slug"         binding:"required"`
	Title        string                 `json:"title"        binding:"required"`
	Text         string                 `json:"text"`
	Summary      string                 `json:"summary"`
	CategoryID   *string                `json:"categoryId"`
	Copyright    *bool                  `json:"copyright"`
	IsPublished  *bool                  `json:"isPublished"`
	AllowComment *bool                  `json:"allowComment"`
	Tags         []string               `json:"tags"`
	Pin          *pinFlag               `json:"pin"`
	PinOrder     *int                   `json:"pinOrder"`
	Images       []models.Image         `json:"images"`
	Meta         map[string]interface{} `json:"meta"`
	Created      *time.Time             `json:"created"`
	RelatedID    []string               `json:"relatedId"`
}

// UpdatePostDTO is the request body for updating a post (all fields optional).
type UpdatePostDTO struct {
	Slug         *string                                `json:"slug"`
	Title        *string                                `json:"title"`
	Text         *string                                `json:"text"`
	Summary      nullable.Value[string]                 `json:"summary"`
	CategoryID   *string                                `json:"categoryId"`
	Copyright    *bool                                  `json:"copyright"`
	IsPublished  *bool                                  `json:"isPublished"`
	AllowComment *bool                                  `json:"allowComment"`
	Tags         []string                               `json:"tags"`
	Pin          *pinFlag                               `json:"pin"`
	PinOrder     *int                                   `json:"pinOrder"`
	Images       []models.Image                         `json:"images"`
	Meta         nullable.Value[map[string]interface{}] `json:"meta"`
	Created      *time.Time                             `json:"created"`
	RelatedID    []string                               `json:"relatedId"`
}

// ListQuery holds query params for listing posts.
type ListQuery struct {
	Year      *int    `form:"year"`
	Category  *string `form:"category"`
	Tag       *string `form:"tag"`
	SortBy    *string `form:"sortBy"`
	SortOrder *int    `form:"sortOrder"`
	Truncate  *int    `form:"truncate"`
}

// postResponse is the API response shape for a post.
type postResponse struct {
	ID           string                 `json:"id"`
	Slug         string                 `json:"slug"`
	Title        string                 `json:"title"`
	Text         string                 `json:"text"`
	Summary      string                 `json:"summary"`
	CategoryID   *string                `json:"categoryId"`
	Category     interface{}            `json:"category"`
	Copyright    bool                   `json:"copyright"`
	IsPublished  bool                   `json:"isPublished"`
	AllowComment bool                   `json:"allowComment"`
	Tags         []string               `json:"tags"`
	Count        models.Count           `json:"count"`
	Pin          *time.Time             `json:"pin"` // pinned date, null when not pinned (same as the original core)
	PinOrder     int                    `json:"pinOrder"`
	Images       []models.Image         `json:"images"`
	Meta         map[string]interface{} `json:"meta,omitempty"`
	Created      time.Time              `json:"created"`
	Modified     *time.Time             `json:"modified"`
	Related      []relatedPost          `json:"related"`
}

type relatedPost struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Slug        string           `json:"slug"`
	Summary     string           `json:"summary"`
	CategoryID  *string          `json:"categoryId"`
	Category    *relatedCategory `json:"category"`
	Created     time.Time        `json:"created"`
	Modified    *time.Time       `json:"modified"`
	isPublished bool
}

type relatedCategory struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func toRelatedPosts(items []models.PostModel) []relatedPost {
	out := make([]relatedPost, 0, len(items))
	for _, item := range items {
		related := relatedPost{
			ID:          item.ID,
			Title:       item.Title,
			Slug:        item.Slug,
			Summary:     item.Summary,
			CategoryID:  item.CategoryID,
			Created:     item.CreatedAt,
			Modified:    models.NullableModified(item.CreatedAt, item.UpdatedAt),
			isPublished: item.IsPublished,
		}
		if item.Category != nil {
			related.Category = &relatedCategory{ID: item.Category.ID, Name: item.Category.Name, Slug: item.Category.Slug}
		}
		out = append(out, related)
	}
	return out
}

// publicRelated drops unpublished related posts for visitors.
func publicRelated(items []relatedPost) []relatedPost {
	out := make([]relatedPost, 0, len(items))
	for _, item := range items {
		if item.isPublished {
			out = append(out, item)
		}
	}
	return out
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
	modified := models.NullableModified(p.CreatedAt, p.UpdatedAt)
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
		Pin:          pinnedAt(p),
		PinOrder:     p.PinOrder,
		Images:       images,
		Meta:         p.Meta,
		Related:      toRelatedPosts(p.Related),
		Created:      p.CreatedAt,
		Modified:     modified,
	}
}

func pinnedAt(p *models.PostModel) *time.Time {
	if !p.Pin {
		return nil
	}
	if p.PinnedAt != nil {
		return p.PinnedAt
	}
	created := p.CreatedAt
	return &created
}

// pinFlag accepts what clients send for "pin": a boolean, or the pinned date / null that
// GET returns (an editor round-tripping the post sends it back).
type pinFlag bool

func (p *pinFlag) UnmarshalJSON(data []byte) error {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch val := v.(type) {
	case bool:
		*p = pinFlag(val)
	case string:
		*p = pinFlag(strings.TrimSpace(val) != "")
	case float64:
		*p = pinFlag(val != 0)
	default:
		*p = false
	}
	return nil
}
