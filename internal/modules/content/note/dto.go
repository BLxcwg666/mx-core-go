package note

import (
	"time"

	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/nullable"
)

type CreateNoteDTO struct {
	Title        string                 `json:"title"       binding:"required"`
	Text         string                 `json:"text"`
	IsPublished  *bool                  `json:"isPublished"`
	AllowComment *bool                  `json:"allowComment"`
	Password     string                 `json:"password"`
	PublicAt     *time.Time             `json:"publicAt"`
	Mood         string                 `json:"mood"`
	Weather      string                 `json:"weather"`
	Bookmark     *bool                  `json:"bookmark"`
	Coordinates  *models.GeoPoint       `json:"coordinates"`
	Location     string                 `json:"location"`
	TopicID      *string                `json:"topicId"`
	Images       []models.Image         `json:"images"`
	Meta         map[string]interface{} `json:"meta"`
	Created      *time.Time             `json:"created"`
}

type UpdateNoteDTO struct {
	Title        *string                                `json:"title"`
	Text         *string                                `json:"text"`
	IsPublished  *bool                                  `json:"isPublished"`
	AllowComment *bool                                  `json:"allowComment"`
	Password     *string                                `json:"password"`
	PublicAt     nullable.Value[time.Time]              `json:"publicAt"`
	Mood         *string                                `json:"mood"`
	Weather      *string                                `json:"weather"`
	Bookmark     *bool                                  `json:"bookmark"`
	Coordinates  nullable.Value[models.GeoPoint]        `json:"coordinates"`
	Location     nullable.Value[string]                 `json:"location"`
	TopicID      nullable.Value[string]                 `json:"topicId"`
	Images       []models.Image                         `json:"images"`
	Meta         nullable.Value[map[string]interface{}] `json:"meta"`
	Created      *time.Time                             `json:"created"`
}

type ListQuery struct {
	Year      *int    `form:"year"`
	SortBy    *string `form:"sortBy"`
	SortOrder *int    `form:"sortOrder"`

	// Filters from the admin list's db_query (only these keys are honoured).
	OnlyBookmark    bool `form:"-"`
	OnlyUnpublished bool `form:"-"`
}

type noteResponse struct {
	ID           string                 `json:"id"`
	NID          int                    `json:"nid"`
	Title        string                 `json:"title"`
	Text         string                 `json:"text"`
	HasPassword  bool                   `json:"hasPassword"`
	IsPublished  bool                   `json:"isPublished"`
	AllowComment bool                   `json:"allowComment"`
	PublicAt     *time.Time             `json:"publicAt"`
	Mood         string                 `json:"mood"`
	Weather      string                 `json:"weather"`
	Bookmark     bool                   `json:"bookmark"`
	Coordinates  *models.GeoPoint       `json:"coordinates"`
	Location     string                 `json:"location"`
	Count        models.Count           `json:"count"`
	TopicID      *string                `json:"topicId"`
	Topic        *noteTopic             `json:"topic"`
	Images       []models.Image         `json:"images"`
	Created      time.Time              `json:"created"`
	Modified     *time.Time             `json:"modified"`
	Meta         map[string]interface{} `json:"meta,omitempty"`
}

type noteTopic struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	Description string     `json:"description"`
	Introduce   string     `json:"introduce"`
	Icon        string     `json:"icon"`
	Created     time.Time  `json:"created"`
	Modified    *time.Time `json:"modified"`
}

func toResponse(n *models.NoteModel, revealProtectedText bool) noteResponse {
	images := n.Images
	if images == nil {
		images = []models.Image{}
	}
	modified := models.NullableModified(n.CreatedAt, n.UpdatedAt)
	text := n.Text
	if n.Password != "" && !revealProtectedText {
		text = ""
	}
	var topic *noteTopic
	if n.Topic != nil {
		topic = &noteTopic{
			ID:          n.Topic.ID,
			Name:        n.Topic.Name,
			Slug:        n.Topic.Slug,
			Description: n.Topic.Description,
			Introduce:   n.Topic.Introduce,
			Icon:        n.Topic.Icon,
			Created:     n.Topic.CreatedAt,
			Modified:    models.NullableModified(n.Topic.CreatedAt, n.Topic.UpdatedAt),
		}
	}
	return noteResponse{
		ID:           n.ID,
		NID:          n.NID,
		Title:        n.Title,
		Text:         text,
		HasPassword:  n.Password != "",
		IsPublished:  n.IsPublished,
		AllowComment: n.AllowComment,
		PublicAt:     n.PublicAt,
		Mood:         n.Mood,
		Weather:      n.Weather,
		Bookmark:     n.Bookmark,
		Coordinates:  n.Coordinates,
		Location:     n.Location,
		Count:        models.Count{Read: n.ReadCount, Like: n.LikeCount},
		TopicID:      n.TopicID,
		Topic:        topic,
		Images:       images,
		Meta:         n.Meta,
		Created:      n.CreatedAt,
		Modified:     modified,
	}
}
