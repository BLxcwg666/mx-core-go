package models

// ActivityModel logs admin activities.
type ActivityModel struct {
	Base
	Type    string                 `json:"type"    gorm:"index;not null"`
	Payload map[string]interface{} `json:"payload" gorm:"type:longtext;serializer:json"`
}

func (ActivityModel) TableName() string { return "activities" }

// SlugTrackerModel tracks slug history for redirects.
type SlugTrackerModel struct {
	Base
	Slug     string `json:"slug"      gorm:"index;not null"`
	Type     string `json:"type"      gorm:"index;not null"` // post | note | page
	TargetID string `json:"target_id" gorm:"index;not null"`
}

func (SlugTrackerModel) TableName() string { return "slug_trackers" }

// FileReferenceModel tracks file uploads and their references.
type FileReferenceModel struct {
	Base
	FileURL  string `json:"file_url"  gorm:"index;not null"`
	FileName string `json:"file_name"`
	Status   string `json:"status"    gorm:"index;default:'pending'"` // pending | active
	RefID    string `json:"ref_id"    gorm:"index"`
	RefType  string `json:"ref_type"  gorm:"index"` // post | note | page | draft
}

func (FileReferenceModel) TableName() string { return "file_references" }

// OptionModel is a generic key-value store for system configuration.
type OptionModel struct {
	ID    uint   `json:"-" gorm:"primaryKey;autoIncrement"`
	Name  string `json:"name"  gorm:"uniqueIndex;not null"`
	Value string `json:"value" gorm:"type:longtext"` // JSON-encoded value
}

func (OptionModel) TableName() string { return "options" }
