package note

import (
	"encoding/json"
	"time"

	"github.com/mx-space/core/internal/models"
)

type CreateNoteDTO struct {
	Title       string           `json:"title"       binding:"required"`
	Text        string           `json:"text"        binding:"required"`
	IsPublished *bool            `json:"is_published"`
	Password    string           `json:"password"`
	PublicAt    *time.Time       `json:"public_at"`
	Mood        string           `json:"mood"`
	Weather     string           `json:"weather"`
	Bookmark    *bool            `json:"bookmark"`
	Coordinates *models.GeoPoint `json:"coordinates"`
	Location    string           `json:"location"`
	TopicID     *string          `json:"topic_id"`
	Images      []models.Image   `json:"images"`
}

func (d *CreateNoteDTO) UnmarshalJSON(data []byte) error {
	type snakeCase CreateNoteDTO
	type camelCase struct {
		IsPublished *bool      `json:"isPublished"`
		PublicAt    *time.Time `json:"publicAt"`
		TopicID     *string    `json:"topicId"`
	}

	var snake snakeCase
	if err := json.Unmarshal(data, &snake); err != nil {
		return err
	}

	var camel camelCase
	if err := json.Unmarshal(data, &camel); err != nil {
		return err
	}

	*d = CreateNoteDTO(snake)
	if d.IsPublished == nil {
		d.IsPublished = camel.IsPublished
	}
	if d.PublicAt == nil {
		d.PublicAt = camel.PublicAt
	}
	if d.TopicID == nil {
		d.TopicID = camel.TopicID
	}

	return nil
}

type UpdateNoteDTO struct {
	Title       *string          `json:"title"`
	Text        *string          `json:"text"`
	IsPublished *bool            `json:"is_published"`
	Password    *string          `json:"password"`
	PublicAt    *time.Time       `json:"public_at"`
	Mood        *string          `json:"mood"`
	Weather     *string          `json:"weather"`
	Bookmark    *bool            `json:"bookmark"`
	Coordinates *models.GeoPoint `json:"coordinates"`
	Location    *string          `json:"location"`
	TopicID     *string          `json:"topic_id"`
	Images      []models.Image   `json:"images"`
}

func (d *UpdateNoteDTO) UnmarshalJSON(data []byte) error {
	type snakeCase UpdateNoteDTO
	type camelCase struct {
		IsPublished *bool      `json:"isPublished"`
		PublicAt    *time.Time `json:"publicAt"`
		TopicID     *string    `json:"topicId"`
	}

	var snake snakeCase
	if err := json.Unmarshal(data, &snake); err != nil {
		return err
	}

	var camel camelCase
	if err := json.Unmarshal(data, &camel); err != nil {
		return err
	}

	*d = UpdateNoteDTO(snake)
	if d.IsPublished == nil {
		d.IsPublished = camel.IsPublished
	}
	if d.PublicAt == nil {
		d.PublicAt = camel.PublicAt
	}
	if d.TopicID == nil {
		d.TopicID = camel.TopicID
	}

	return nil
}

type noteResponse struct {
	ID          string           `json:"id"`
	NID         int              `json:"nid"`
	Title       string           `json:"title"`
	Text        string           `json:"text"`
	IsPublished bool             `json:"is_published"`
	PublicAt    *time.Time       `json:"public_at"`
	Mood        string           `json:"mood"`
	Weather     string           `json:"weather"`
	Bookmark    bool             `json:"bookmark"`
	Coordinates *models.GeoPoint `json:"coordinates"`
	Location    string           `json:"location"`
	Count       models.Count     `json:"count"`
	TopicID     *string          `json:"topic_id"`
	Topic       interface{}      `json:"topic"`
	Images      []models.Image   `json:"images"`
	Created     time.Time        `json:"created"`
	Modified    time.Time        `json:"modified"`
}

func toResponse(n *models.NoteModel) noteResponse {
	images := n.Images
	if images == nil {
		images = []models.Image{}
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
		Topic:       n.Topic,
		Images:      images,
		Created:     n.CreatedAt,
		Modified:    n.UpdatedAt,
	}
}
