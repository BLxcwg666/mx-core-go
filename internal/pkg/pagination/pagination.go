package pagination

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

const (
	DefaultPage = 1
	DefaultSize = 10
	MaxSize     = 100
)

// Query holds parsed pagination parameters.
type Query struct {
	Page int
	Size int
}

// FromContext extracts and validates pagination params from the request.
func FromContext(c *gin.Context) Query {
	page := parseIntOr(c.DefaultQuery("page", "1"), DefaultPage)
	size := parseIntOr(c.DefaultQuery("size", "10"), DefaultSize)

	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = DefaultSize
	}
	if size > MaxSize {
		size = MaxSize
	}

	return Query{Page: page, Size: size}
}

// Paginate applies limit/offset to a GORM query and returns the pagination metadata.
func Paginate[T any](db *gorm.DB, q Query, dest *[]T) (response.Pagination, error) {
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return response.Pagination{}, err
	}

	offset := (q.Page - 1) * q.Size
	if err := db.Offset(offset).Limit(q.Size).Find(dest).Error; err != nil {
		return response.Pagination{}, err
	}

	totalPage := int((total + int64(q.Size) - 1) / int64(q.Size))

	return response.Pagination{
		Total:       total,
		CurrentPage: q.Page,
		TotalPage:   totalPage,
		Size:        q.Size,
		HasNextPage: q.Page < totalPage,
	}, nil
}

func parseIntOr(s string, def int) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
