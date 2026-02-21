package models

// MetaFieldOption is an option item for selectable meta preset fields.
type MetaFieldOption struct {
	Value     interface{} `json:"value"`
	Label     string      `json:"label"`
	Exclusive bool        `json:"exclusive,omitempty"`
}

// MetaPresetChild defines a nested field for object-type meta presets.
type MetaPresetChild struct {
	Key         string            `json:"key"`
	Label       string            `json:"label"`
	Type        string            `json:"type"`
	Description string            `json:"description,omitempty"`
	Placeholder string            `json:"placeholder,omitempty"`
	Options     []MetaFieldOption `json:"options,omitempty" gorm:"serializer:json;type:longtext"`
}

// MetaPresetModel stores reusable metadata field definitions.
type MetaPresetModel struct {
	Base
	Key               string            `json:"key"                  gorm:"uniqueIndex;not null"`
	Label             string            `json:"label"                gorm:"not null"`
	Type              string            `json:"type"                 gorm:"not null"`
	Description       string            `json:"description"`
	Placeholder       string            `json:"placeholder"`
	Scope             string            `json:"scope"                gorm:"index;default:'both'"`
	Options           []MetaFieldOption `json:"options,omitempty"    gorm:"serializer:json;type:longtext"`
	AllowCustomOption bool              `json:"allowCustomOption"`
	Children          []MetaPresetChild `json:"children,omitempty"   gorm:"serializer:json;type:longtext"`
	IsBuiltin         bool              `json:"isBuiltin"`
	Order             int               `json:"order"                gorm:"index;default:0"`
	Enabled           bool              `json:"enabled"              gorm:"default:true"`
}

func (MetaPresetModel) TableName() string { return "meta_presets" }
