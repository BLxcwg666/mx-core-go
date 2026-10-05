package models

import (
	"time"

	"gorm.io/gorm"
)

// CommentState represents the moderation state of a comment.
type CommentState int

const (
	CommentUnread CommentState = 0
	CommentRead   CommentState = 1
	CommentJunk   CommentState = 2
)

// RefType indicates which content type a comment is attached to.
type RefType string

const (
	RefTypePost     RefType = "post"
	RefTypeNote     RefType = "note"
	RefTypePage     RefType = "page"
	RefTypeRecently RefType = "recently"
)

// CommentModel represents a comment on any content type.
type CommentModel struct {
	Base
	RefType       RefType                `json:"ref_type"       gorm:"not null;index"`
	RefID         string                 `json:"ref_id"         gorm:"not null;index"`
	Author        string                 `json:"author"         gorm:"not null"`
	Mail          string                 `json:"mail"`
	URL           string                 `json:"url" gorm:"type:text"`
	Text          string                 `json:"text"           gorm:"type:text;not null"`
	State         CommentState           `json:"state"          gorm:"default:0;index"`
	ParentID      *string                `json:"parent_id"      gorm:"index"`
	Children      []CommentModel         `json:"children,omitempty" gorm:"foreignKey:ParentID"`
	CommentsIndex int                    `json:"comments_index" gorm:"default:0"`
	Key           string                 `json:"key"`
	IP            string                 `json:"ip"`
	Agent         string                 `json:"agent"          gorm:"type:varchar(512)"`
	Pin           bool                   `json:"pin"            gorm:"default:false"`
	IsWhispers    bool                   `json:"is_whispers"    gorm:"default:false"`
	Avatar        string                 `json:"avatar" gorm:"type:text"`
	Location      string                 `json:"location"`
	Meta          map[string]interface{} `json:"meta,omitempty" gorm:"type:longtext;serializer:json"`
	ReaderID      *string                `json:"reader_id"      gorm:"index"`
	EditedAt      *time.Time             `json:"edited_at"`
	Source        string                 `json:"source"`
}

func (CommentModel) TableName() string { return "comments" }

// DeleteCommentsByRef removes every comment and reply attached to a content item, like the original core does
// when the item is deleted; orphaned comments break the admin comment list.
func DeleteCommentsByRef(db *gorm.DB, refType RefType, refID string) error {
	return db.Where("ref_type = ? AND ref_id = ?", refType, refID).Delete(&CommentModel{}).Error
}
