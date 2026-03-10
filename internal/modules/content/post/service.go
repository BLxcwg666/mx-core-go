package post

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/system/util/slugtracker"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

// Service handles post business logic.
type Service struct {
	db          *gorm.DB
	slugTracker *slugtracker.Service
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

// SetSlugTracker wires up slug change tracking (optional).
func (s *Service) SetSlugTracker(st *slugtracker.Service) { s.slugTracker = st }

// List returns a paginated list of posts.
func (s *Service) List(q pagination.Query, lq ListQuery) ([]models.PostModel, response.Pagination, error) {
	tx := s.db.Model(&models.PostModel{}).
		Preload("Category")

	if lq.Year != nil {
		tx = tx.Where("YEAR(created_at) = ?", *lq.Year)
	}
	if lq.Category != nil {
		tx = tx.Joins("JOIN categories ON categories.id = posts.category_id").
			Where("categories.slug = ?", *lq.Category)
	}
	if lq.Tag != nil {
		tx = tx.Where("JSON_CONTAINS(tags, ?)", fmt.Sprintf("%q", *lq.Tag))
	}

	orders, needsCategoryJoin := postListOrders(lq)
	if needsCategoryJoin && lq.Category == nil {
		tx = tx.Joins("LEFT JOIN categories ON categories.id = posts.category_id")
	}
	for _, order := range orders {
		tx = tx.Order(order)
	}

	var posts []models.PostModel
	pag, err := pagination.Paginate(tx, q, &posts)
	return posts, pag, err
}

func postListOrders(lq ListQuery) ([]string, bool) {
	sortBy := ""
	if lq.SortBy != nil {
		sortBy = normalizePostSortKey(*lq.SortBy)
	}
	if sortBy == "" {
		return []string{"pin_order DESC", "created_at DESC"}, false
	}

	direction := "DESC"
	if lq.SortOrder != nil && *lq.SortOrder == 1 {
		direction = "ASC"
	}

	switch sortBy {
	case "created", "createdat":
		return []string{"created_at " + direction}, false
	case "modified", "updated", "updatedat":
		return []string{"updated_at " + direction}, false
	case "title":
		return []string{"title " + direction}, false
	case "slug":
		return []string{"slug " + direction}, false
	case "summary":
		return []string{"summary " + direction}, false
	case "copyright":
		return []string{"copyright " + direction}, false
	case "ispublished":
		return []string{"is_published " + direction}, false
	case "allowcomment":
		return []string{"allow_comment " + direction}, false
	case "pin":
		return []string{"pin " + direction}, false
	case "pinorder":
		return []string{"pin_order " + direction}, false
	case "read", "readcount", "countread":
		return []string{"read_count " + direction}, false
	case "like", "likecount", "countlike":
		return []string{"like_count " + direction}, false
	case "category", "categoryname":
		return []string{"categories.name " + direction}, true
	default:
		return []string{"pin_order DESC", "created_at DESC"}, false
	}
}

func normalizePostSortKey(sortBy string) string {
	replacer := strings.NewReplacer("_", "", "-", "", ".", "", " ", "")
	return strings.ToLower(replacer.Replace(strings.TrimSpace(sortBy)))
}

// GetBySlug fetches a single post by slug.
func (s *Service) GetBySlug(slug string, isAdmin bool) (*models.PostModel, error) {
	var post models.PostModel
	tx := s.db.Preload("Category").Preload("Related").Where("slug = ?", slug)
	if !isAdmin {
		tx = tx.Where("is_published = ?", true)
	}
	if err := tx.First(&post).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &post, nil
}

// GetByCategoryAndSlug fetches a post by category slug and post slug.
func (s *Service) GetByCategoryAndSlug(categorySlug, slug string, isAdmin bool) (*models.PostModel, error) {
	var post models.PostModel
	tx := s.db.
		Model(&models.PostModel{}).
		Preload("Category").
		Preload("Related").
		Joins("JOIN categories ON categories.id = posts.category_id").
		Where("categories.slug = ? AND posts.slug = ?", categorySlug, slug)
	if !isAdmin {
		tx = tx.Where("posts.is_published = ?", true)
	}
	if err := tx.First(&post).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &post, nil
}

// GetByID fetches a single post by ID.
func (s *Service) GetByID(id string) (*models.PostModel, error) {
	var post models.PostModel
	if err := s.db.Preload("Category").First(&post, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &post, nil
}

// GetByIdentifier fetches a post by ID first, then falls back to slug.
func (s *Service) GetByIdentifier(identifier string, isAdmin bool) (*models.PostModel, error) {
	if post, err := s.GetByID(identifier); err != nil {
		return nil, err
	} else if post != nil {
		if !isAdmin && !post.IsPublished {
			return nil, nil
		}
		return post, nil
	}
	return s.GetBySlug(identifier, isAdmin)
}

func (s *Service) GetLatest(isAdmin bool) (*models.PostModel, error) {
	var post models.PostModel
	tx := s.db.Preload("Category").Order("created_at DESC")
	if !isAdmin {
		tx = tx.Where("is_published = ?", true)
	}
	if err := tx.First(&post).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &post, nil
}

// Create inserts a new post.
func (s *Service) Create(dto *CreatePostDTO) (*models.PostModel, error) {
	var count int64
	s.db.Model(&models.PostModel{}).Where("slug = ?", dto.Slug).Count(&count)
	if count > 0 {
		return nil, fmt.Errorf("slug already exists")
	}

	categoryID := ""
	if dto.CategoryID != nil {
		categoryID = strings.TrimSpace(*dto.CategoryID)
	}
	if categoryID == "" {
		return nil, fmt.Errorf("category is required")
	}

	var category models.CategoryModel
	if err := s.db.First(&category, "id = ?", categoryID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("category not found")
		}
		return nil, err
	}

	post := models.PostModel{
		WriteBase: models.WriteBase{
			Title:  dto.Title,
			Text:   dto.Text,
			Images: dto.Images,
		},
		Slug:       dto.Slug,
		Summary:    dto.Summary,
		CategoryID: &category.ID,
		Tags:       dto.Tags,
	}
	if dto.Copyright != nil {
		post.Copyright = *dto.Copyright
	} else {
		post.Copyright = true
	}
	if dto.IsPublished != nil {
		post.IsPublished = *dto.IsPublished
	} else {
		post.IsPublished = true
	}
	if dto.AllowComment != nil {
		post.AllowComment = *dto.AllowComment
	} else {
		post.AllowComment = true
	}
	if dto.Pin != nil {
		post.Pin = *dto.Pin
	}
	if dto.PinOrder != nil {
		post.PinOrder = *dto.PinOrder
	}

	if err := s.db.Create(&post).Error; err != nil {
		return nil, err
	}
	if err := s.db.Preload("Category").First(&post, "id = ?", post.ID).Error; err != nil {
		return nil, err
	}
	return &post, nil
}

// Update patches a post by ID.
func (s *Service) Update(id string, dto *UpdatePostDTO) (*models.PostModel, error) {
	post, err := s.GetByID(id)
	if err != nil {
		return nil, err
	}
	if post == nil {
		return nil, nil
	}

	updates := map[string]interface{}{}
	var oldSlug string
	if dto.Slug != nil && *dto.Slug != post.Slug {
		oldSlug = post.Slug
		updates["slug"] = *dto.Slug
	}
	if dto.Title != nil {
		updates["title"] = *dto.Title
	}
	if dto.Text != nil {
		updates["text"] = *dto.Text
	}
	if dto.Summary != nil {
		updates["summary"] = *dto.Summary
	}
	if dto.CategoryID != nil {
		categoryID := strings.TrimSpace(*dto.CategoryID)
		if categoryID == "" {
			return nil, fmt.Errorf("category is required")
		}

		var category models.CategoryModel
		if err := s.db.First(&category, "id = ?", categoryID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("category not found")
			}
			return nil, err
		}
		updates["category_id"] = category.ID
	}
	if dto.Copyright != nil {
		updates["copyright"] = *dto.Copyright
	}
	if dto.IsPublished != nil {
		updates["is_published"] = *dto.IsPublished
	}
	if dto.AllowComment != nil {
		updates["allow_comment"] = *dto.AllowComment
	}
	if dto.Tags != nil {
		encodedTags, err := json.Marshal(dto.Tags)
		if err != nil {
			return nil, err
		}
		updates["tags"] = string(encodedTags)
	}
	if dto.Pin != nil {
		updates["pin"] = *dto.Pin
	}
	if dto.PinOrder != nil {
		updates["pin_order"] = *dto.PinOrder
	}
	if dto.Images != nil {
		encodedImages, err := json.Marshal(dto.Images)
		if err != nil {
			return nil, err
		}
		updates["images"] = string(encodedImages)
	}

	if err := s.db.Model(post).Updates(updates).Error; err != nil {
		return nil, err
	}

	if oldSlug != "" && s.slugTracker != nil {
		go s.slugTracker.Track(oldSlug, "post", post.ID) // nolint:errcheck
	}
	return s.GetByID(post.ID)
}

// Delete soft-deletes a post by ID.
func (s *Service) Delete(id string) error {
	if s.slugTracker != nil {
		go s.slugTracker.DeleteByTargetID(id) // nolint:errcheck
	}
	return s.db.Delete(&models.PostModel{}, "id = ?", id).Error
}

// IncrementReadCount atomically increments the read counter.
func (s *Service) IncrementReadCount(id string) error {
	return s.db.Model(&models.PostModel{}).Where("id = ?", id).
		UpdateColumn("read_count", gorm.Expr("read_count + 1")).Error
}

// IncrementLikeCount atomically increments the like counter.
func (s *Service) IncrementLikeCount(id string) error {
	return s.db.Model(&models.PostModel{}).Where("id = ?", id).
		UpdateColumn("like_count", gorm.Expr("like_count + 1")).Error
}
