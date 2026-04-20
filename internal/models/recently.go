package models

// RecentlyModel is a short-form post (micro-blog).
type RecentlyModel struct {
	Base
	Content       string      `json:"content"        gorm:"type:text;not null"`
	Type          string      `json:"type"           gorm:"type:varchar(32);default:text"`
	Metadata      JSONMap     `json:"metadata,omitempty" gorm:"serializer:json"`
	RefType       *RefType    `json:"ref_type,omitempty"  gorm:"type:varchar(32)"`
	RefID         *string     `json:"ref_id,omitempty"    gorm:"index"`
	Modified      interface{} `json:"modified,omitempty"   gorm:"-"`
	UpCount       int         `json:"up"             gorm:"column:up_count;default:0"`
	DownCount     int         `json:"down"           gorm:"column:down_count;default:0"`
	CommentsIndex int         `json:"comments_index" gorm:"default:0"`
	AllowComment  bool        `json:"allow_comment"  gorm:"default:true"`
}

func (RecentlyModel) TableName() string { return "recentlies" }
