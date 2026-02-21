package models

import "time"

// DraftRefType indicates the content type a draft belongs to.
type DraftRefType string

const (
	DraftRefPost DraftRefType = "posts"
	DraftRefNote DraftRefType = "notes"
	DraftRefPage DraftRefType = "pages"

	// compatibility
	DraftRefPostLegacy DraftRefType = "post"
	DraftRefNoteLegacy DraftRefType = "note"
	DraftRefPageLegacy DraftRefType = "page"
)

// DraftModel stores versioned drafts for posts, notes, and pages.
type DraftModel struct {
	Base
	RefType          DraftRefType           `json:"refType"           gorm:"index"`
	RefID            *string                `json:"refId"             gorm:"index"`
	Title            string                 `json:"title"`
	Text             string                 `json:"text"              gorm:"type:longtext"`
	Images           []Image                `json:"images"            gorm:"serializer:json"`
	Meta             map[string]interface{} `json:"meta,omitempty"    gorm:"serializer:json"`
	TypeSpecificData map[string]interface{} `json:"typeSpecificData,omitempty" gorm:"serializer:json"`
	Version          int                    `json:"version"           gorm:"default:0"`
	PublishedVersion *int                   `json:"publishedVersion"`

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
	TypeSpecificData map[string]interface{} `json:"typeSpecificData,omitempty" gorm:"serializer:json"`
	SavedAt          time.Time              `json:"savedAt"`
	IsFullSnapshot   bool                   `json:"isFullSnapshot"  gorm:"default:true"`
	RefVersion       *int                   `json:"refVersion"`
	BaseVersion      *int                   `json:"baseVersion"`
}

func (DraftHistoryModel) TableName() string { return "draft_histories" }
