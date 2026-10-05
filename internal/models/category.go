package models

// CategoryModel represents a post category or tag.
type CategoryModel struct {
	Base
	Name string `json:"name"  gorm:"uniqueIndex;not null"`
	Slug string `json:"slug"  gorm:"uniqueIndex;not null"`
	Type int    `json:"type"  gorm:"default:0"`

	Posts []PostModel `json:"posts,omitempty" gorm:"foreignKey:CategoryID"`
}

func (CategoryModel) TableName() string { return "categories" }

// TopicModel groups notes together.
type TopicModel struct {
	Base
	Name        string `json:"name"        gorm:"uniqueIndex;not null"`
	Slug        string `json:"slug"        gorm:"uniqueIndex;not null"`
	Description string `json:"description" gorm:"type:text"`
	Introduce   string `json:"introduce" gorm:"type:text"`
	Icon        string `json:"icon" gorm:"type:text"`

	Notes []NoteModel `json:"notes,omitempty" gorm:"foreignKey:TopicID"`
}

func (TopicModel) TableName() string { return "topics" }
