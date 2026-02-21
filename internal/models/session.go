package models

import "time"

// UserSession tracks signed-in JWT sessions for device/session management.
type UserSession struct {
	Base
	UserID    string     `json:"user_id"    gorm:"index;not null"`
	IP        string     `json:"ip"`
	UA        string     `json:"ua"         gorm:"type:text"`
	ExpiresAt time.Time  `json:"expires_at" gorm:"index;not null"`
	RevokedAt *time.Time `json:"revoked_at" gorm:"index"`
}

func (UserSession) TableName() string { return "user_sessions" }
