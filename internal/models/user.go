package models

import "time"

// UserModel represents a blog owner/admin.
type UserModel struct {
	Base
	Username      string        `json:"username"        gorm:"uniqueIndex;not null"`
	Name          string        `json:"name"`
	Introduce     string        `json:"introduce"`
	Avatar        string        `json:"avatar"`
	Password      string        `json:"-"               gorm:"not null"`
	Mail          string        `json:"mail"`
	URL           string        `json:"url"`
	SocialIDs     string        `json:"-"               gorm:"type:longtext"`
	LastLoginTime *time.Time    `json:"last_login_time"`
	LastLoginIP   string        `json:"last_login_ip"`
	APITokens     []APIToken    `json:"api_tokens,omitempty" gorm:"foreignKey:UserID"`
	OAuth2        []OAuth2Token `json:"oauth2,omitempty"     gorm:"foreignKey:UserID"`
}

func (UserModel) TableName() string { return "users" }

// APIToken represents a personal API token for programmatic access.
type APIToken struct {
	Base
	UserID    string     `json:"-"          gorm:"index;not null"`
	Token     string     `json:"token"      gorm:"uniqueIndex;not null"`
	Name      string     `json:"name"`
	ExpiredAt *time.Time `json:"expired_at"`
}

func (APIToken) TableName() string { return "api_tokens" }

// OAuth2Token holds OAuth2 account info linked to a user.
type OAuth2Token struct {
	Base
	UserID      string     `json:"-"           gorm:"index;not null"`
	Provider    string     `json:"provider"    gorm:"index;not null"`
	ProviderUID string     `json:"provider_uid" gorm:"index"`
	AccessToken string     `json:"-"           gorm:"type:text"`
	LastUsed    *time.Time `json:"last_used"`
}

func (OAuth2Token) TableName() string { return "oauth2_tokens" }

// AuthnModel stores WebAuthn/passkey credentials.
type AuthnModel struct {
	Base
	Name                 string `json:"name"                    gorm:"uniqueIndex;not null"`
	CredentialID         []byte `json:"-"                       gorm:"type:blob"`
	CredentialPublicKey  []byte `json:"-"                       gorm:"type:blob"`
	Counter              uint32 `json:"counter"`
	CredentialDeviceType string `json:"credential_device_type"`
	CredentialBackedUp   bool   `json:"credential_backed_up"`
}

func (AuthnModel) TableName() string { return "authn_credentials" }

// ReaderModel tracks OAuth comment readers.
type ReaderModel struct {
	Base
	Email   string `json:"email"    gorm:"uniqueIndex"`
	Name    string `json:"name"`
	Handle  string `json:"handle"`
	Image   string `json:"image"`
	IsOwner bool   `json:"is_owner"`
}

func (ReaderModel) TableName() string { return "readers" }
