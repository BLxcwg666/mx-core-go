package models

// StringSlice is a []string that serializes as JSON in MySQL.
type StringSlice []string

// PostModel is a blog post.
type PostModel struct {
	WriteBase
	Slug        string         `json:"slug"         gorm:"uniqueIndex;not null"`
	Summary     string         `json:"summary"`
	CategoryID  *string        `json:"category_id"  gorm:"index"`
	Category    *CategoryModel `json:"category,omitempty"  gorm:"foreignKey:CategoryID"`
	Copyright   bool           `json:"copyright"    gorm:"default:true"`
	IsPublished bool           `json:"is_published" gorm:"default:false;index"`
	Tags        StringSlice    `json:"tags"         gorm:"type:json;serializer:json"`
	ReadCount   int            `json:"read"         gorm:"column:read_count;default:0"`
	LikeCount   int            `json:"like"         gorm:"column:like_count;default:0"`
	Pin         bool           `json:"pin"          gorm:"default:false"`
	PinOrder    int            `json:"pin_order"    gorm:"default:0"`

	// many2many self-reference for related posts
	Related []PostModel `json:"related,omitempty" gorm:"many2many:post_related;joinForeignKey:PostID;joinReferences:RelatedPostID"`
}

func (PostModel) TableName() string { return "posts" }

// Count returns the embedded count object expected by the API.
func (p PostModel) GetCount() Count {
	return Count{Read: p.ReadCount, Like: p.LikeCount}
}
