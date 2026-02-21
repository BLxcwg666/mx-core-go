package models

import "time"

// DraftRefType indicates the content type a draft belongs to.
type DraftRefType string

const (
	DraftRefPost DraftRefType = "post"
	DraftRefNote DraftRefType = "note"
	DraftRefPage DraftRefType = "page"
)

// DraftModel stores versioned drafts for posts, notes, and pages.
type DraftModel struct {
	Base
	RefType          DraftRefType           `json:"ref_type"          gorm:"index"`
	RefID            *string                `json:"ref_id"            gorm:"index"`
	Title            string                 `json:"title"`
	Text             string                 `json:"text"              gorm:"type:longtext"`
	Images           []Image                `json:"images"            gorm:"serializer:json"`
	Meta             map[string]interface{} `json:"meta,omitempty"    gorm:"serializer:json"`
	TypeSpecificData map[string]interface{} `json:"type_specific_data,omitempty" gorm:"serializer:json"`
	Version          int                    `json:"version"           gorm:"default:0"`
	PublishedVersion *int                   `json:"published_version"`

	History []DraftHistoryModel `json:"history,omitempty" gorm:"foreignKey:DraftID"`
}

func (DraftModel) TableName() string { return "drafts" }

// DraftHistoryModel stores a historical snapshot of a draft.
type DraftHistoryModel struct {
	Base
	DraftID          string                 `json:"-"                 gorm:"index;not null"`
	Version          int                    `json:"version"`
	Title            string                 `json:"title"`
	Text             string                 `json:"text"              gorm:"type:longtext"`
	TypeSpecificData map[string]interface{} `json:"type_specific_data,omitempty" gorm:"serializer:json"`
	SavedAt          time.Time              `json:"saved_at"`
	IsFullSnapshot   bool                   `json:"is_full_snapshot"  gorm:"default:true"`
	RefVersion       *int                   `json:"ref_version"`
	BaseVersion      *int                   `json:"base_version"`
}

func (DraftHistoryModel) TableName() string { return "draft_histories" }
