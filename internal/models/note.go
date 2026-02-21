package models

import "time"

// NoteModel is a diary/note entry.
type NoteModel struct {
	WriteBase
	NID         int         `json:"nid"          gorm:"column:n_id;uniqueIndex;not null"` // numeric sequential ID, managed by service
	IsPublished bool        `json:"is_published" gorm:"default:false;index"`
	Password    string      `json:"-"            gorm:"column:password_hash"` // bcrypt, never exposed
	PublicAt    *time.Time  `json:"public_at"`
	Mood        string      `json:"mood"`
	Weather     string      `json:"weather"`
	Bookmark    bool        `json:"bookmark"     gorm:"default:false"`
	Coordinates *GeoPoint   `json:"coordinates,omitempty" gorm:"serializer:json"`
	Location    string      `json:"location"`
	ReadCount   int         `json:"read"         gorm:"column:read_count;default:0"`
	LikeCount   int         `json:"like"         gorm:"column:like_count;default:0"`
	TopicID     *string     `json:"topic_id"     gorm:"index"`
	Topic       *TopicModel `json:"topic,omitempty" gorm:"foreignKey:TopicID"`
}

func (NoteModel) TableName() string { return "notes" }

// GeoPoint represents a geographic coordinate.
type GeoPoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}
