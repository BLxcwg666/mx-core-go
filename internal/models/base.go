package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Base is the base model for all entities.
// ID is a UUID string for API compatibility with the original MongoDB ObjectID format.
// Deletes are hard deletes: soft-deleted rows kept their unique keys (slug, name, ...) and blocked re-creation.
type Base struct {
	ID        string    `json:"id"       gorm:"type:char(36);primaryKey"`
	CreatedAt time.Time `json:"created"`
	UpdatedAt time.Time `json:"modified"`
}

func NullableModified(created, updated time.Time) *time.Time {
	if updated.IsZero() || updated.Year() <= 1 || !updated.After(created) {
		return nil
	}
	modifiedAt := updated
	return &modifiedAt
}

func (b *Base) BeforeCreate(tx *gorm.DB) error {
	if b.ID == "" {
		b.ID = uuid.New().String()
	}
	return nil
}

// WriteBase adds text/images/meta common to Post, Note, Page.
type WriteBase struct {
	Base
	Title  string  `json:"title"  gorm:"not null"`
	Text   string  `json:"text"   gorm:"type:longtext"`
	Images []Image `json:"images" gorm:"type:longtext;serializer:json"`
}

// Image represents an embedded image reference.
type Image struct {
	Name     string `json:"name"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	Type     string `json:"type,omitempty"`
	Src      string `json:"src"`
	Accent   string `json:"accent,omitempty"`
	Blurhash string `json:"blur_hash,omitempty"`
}

// Count tracks read and like counts for content.
type Count struct {
	Read int `json:"read"`
	Like int `json:"like"`
}

// JSONMap stores arbitrary JSON object data.
type JSONMap map[string]interface{}

// UnmarshalJSON accepts both "blur_hash" and "blurHash": the admin panel sends camelCase,
// and images restored from the original core are stored with "blurHash".
func (img *Image) UnmarshalJSON(data []byte) error {
	type plain Image
	var aux struct {
		plain
		BlurHashCamel string `json:"blurHash"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*img = Image(aux.plain)
	if img.Blurhash == "" {
		img.Blurhash = aux.BlurHashCamel
	}
	return nil
}
