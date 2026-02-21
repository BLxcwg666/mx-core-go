package models

// SayModel stores quotes/sayings.
type SayModel struct {
	Base
	Text   string `json:"text"   gorm:"type:text;not null"`
	Source string `json:"source"`
	Author string `json:"author"`
}

func (SayModel) TableName() string { return "says" }
