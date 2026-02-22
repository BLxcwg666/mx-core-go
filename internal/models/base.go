package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Base is the base model for all entities.
// ID is a UUID string for API compatibility with the original MongoDB ObjectID format.
type Base struct {
	ID        string         `json:"id"       gorm:"type:char(36);primaryKey"`
	CreatedAt time.Time      `json:"created"`
	UpdatedAt time.Time      `json:"modified"`
	DeletedAt gorm.DeletedAt `json:"-"        gorm:"index"`
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
