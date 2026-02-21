package models

// ProjectModel stores personal projects.
type ProjectModel struct {
	Base
	Name        string      `json:"name"        gorm:"uniqueIndex;not null"`
	PreviewURL  string      `json:"preview_url"`
	DocURL      string      `json:"doc_url"`
	ProjectURL  string      `json:"project_url"`
	Images      StringArray `json:"images"      gorm:"type:longtext"`
	Description string      `json:"description"`
	Avatar      string      `json:"avatar"`
	Text        string      `json:"text"        gorm:"type:text"`
}

func (ProjectModel) TableName() string { return "projects" }
