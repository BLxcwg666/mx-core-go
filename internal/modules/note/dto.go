package note

import (
	"time"

	"github.com/mx-space/core/internal/models"
)

type CreateNoteDTO struct {
	Title       string           `json:"title"       binding:"required"`
	Text        string           `json:"text"        binding:"required"`
	IsPublished *bool            `json:"isPublished"`
	Password    string           `json:"password"`
	PublicAt    *time.Time       `json:"publicAt"`
	Mood        string           `json:"mood"`
	Weather     string           `json:"weather"`
	Bookmark    *bool            `json:"bookmark"`
	Coordinates *models.GeoPoint `json:"coordinates"`
	Location    string           `json:"location"`
	TopicID     *string          `json:"topicId"`
	Images      []models.Image   `json:"images"`
}

type UpdateNoteDTO struct {
	Title       *string          `json:"title"`
	Text        *string          `json:"text"`
	IsPublished *bool            `json:"isPublished"`
	Password    *string          `json:"password"`
	PublicAt    *time.Time       `json:"publicAt"`
	Mood        *string          `json:"mood"`
	Weather     *string          `json:"weather"`
	Bookmark    *bool            `json:"bookmark"`
	Coordinates *models.GeoPoint `json:"coordinates"`
	Location    *string          `json:"location"`
	TopicID     *string          `json:"topicId"`
	Images      []models.Image   `json:"images"`
}

type noteResponse struct {
	ID          string           `json:"id"`
	NID         int              `json:"nid"`
	Title       string           `json:"title"`
	Text        string           `json:"text"`
	IsPublished bool             `json:"isPublished"`
	PublicAt    *time.Time       `json:"publicAt"`
	Mood        string           `json:"mood"`
	Weather     string           `json:"weather"`
	Bookmark    bool             `json:"bookmark"`
	Coordinates *models.GeoPoint `json:"coordinates"`
	Location    string           `json:"location"`
	Count       models.Count     `json:"count"`
	TopicID     *string          `json:"topicId"`
	Topic       *noteTopic       `json:"topic"`
	Images      []models.Image   `json:"images"`
	Created     time.Time        `json:"created"`
	Modified    *time.Time       `json:"modified"`
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

func nullableModified(t time.Time) *time.Time {
	if t.IsZero() || t.Year() <= 1 {
		return nil
	}
	modifiedAt := t
	return &modifiedAt
}

func toResponse(n *models.NoteModel) noteResponse {
	images := n.Images
	if images == nil {
		images = []models.Image{}
	}
	modified := nullableModified(n.UpdatedAt)
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
			Modified:    nullableModified(n.Topic.UpdatedAt),
		}
	}
	return noteResponse{
		ID:          n.ID,
		NID:         n.NID,
		Title:       n.Title,
		Text:        n.Text,
		IsPublished: n.IsPublished,
		PublicAt:    n.PublicAt,
		Mood:        n.Mood,
		Weather:     n.Weather,
		Bookmark:    n.Bookmark,
		Coordinates: n.Coordinates,
		Location:    n.Location,
		Count:       models.Count{Read: n.ReadCount, Like: n.LikeCount},
		TopicID:     n.TopicID,
		Topic:       topic,
		Images:      images,
		Created:     n.CreatedAt,
		Modified:    modified,
	}
}
