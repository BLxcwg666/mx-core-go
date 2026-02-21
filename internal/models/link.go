package models

// LinkState represents the approval state of a friend link.
type LinkState int

const (
	LinkPass    LinkState = 0
	LinkAudit   LinkState = 1
	LinkOutdate LinkState = 2
	LinkBanned  LinkState = 3
	LinkReject  LinkState = 4
)

// LinkType classifies the friend link.
type LinkType int

const (
	LinkTypeFriend     LinkType = 0
	LinkTypeCollection LinkType = 1
)

// LinkModel stores friend/collection links.
type LinkModel struct {
	Base
	Name        string    `json:"name"        gorm:"uniqueIndex;not null"`
	URL         string    `json:"url"         gorm:"uniqueIndex;not null"`
	Avatar      string    `json:"avatar"`
	Description string    `json:"description"`
	Type        LinkType  `json:"type"        gorm:"default:0"`
	State       LinkState `json:"state"       gorm:"default:1;index"`
	Email       string    `json:"email"`
}

func (LinkModel) TableName() string { return "links" }
