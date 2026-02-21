package models

// SubscribeModel manages email subscriptions.
type SubscribeModel struct {
	Base
	Email       string `json:"email"        gorm:"uniqueIndex;not null"`
	CancelToken string `json:"-"            gorm:"uniqueIndex"`
	Subscribe   int    `json:"subscribe"    gorm:"default:0"` // bitmask
	Verified    bool   `json:"verified"     gorm:"default:false"`
}

func (SubscribeModel) TableName() string { return "subscribes" }
