package models

// ProjectModel stores personal projects.
type ProjectModel struct {
	Base
	Name        string      `json:"name"        gorm:"uniqueIndex;not null"`
	PreviewURL  string      `json:"preview_url" gorm:"type:text"`
	DocURL      string      `json:"doc_url" gorm:"type:text"`
	ProjectURL  string      `json:"project_url" gorm:"type:text"`
	Images      StringArray `json:"images"      gorm:"type:longtext"`
	Description string      `json:"description" gorm:"type:text"`
	Avatar      string      `json:"avatar" gorm:"type:text"`
	Text        string      `json:"text"        gorm:"type:longtext"`
}

func (ProjectModel) TableName() string { return "projects" }
