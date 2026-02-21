package models

// PageModel is a static page (e.g. About, Contact).
type PageModel struct {
	WriteBase
	Slug          string                 `json:"slug"            gorm:"uniqueIndex;not null"`
	Subtitle      string                 `json:"subtitle"`
	Order         int                    `json:"order"           gorm:"column:order_num;default:0"`
	Meta          map[string]interface{} `json:"meta,omitempty"  gorm:"serializer:json"`
	AllowComment  bool                   `json:"allow_comment"   gorm:"default:true"`
	CommentsIndex int                    `json:"comments_index"  gorm:"default:0"`
	ReadCount     int64                  `json:"read_count"      gorm:"column:read_count;default:0"`
}

func (PageModel) TableName() string { return "pages" }
