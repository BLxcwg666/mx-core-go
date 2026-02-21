package post

import (
	"errors"
	"fmt"

	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/slugtracker"
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
		Preload("Category").
		Order("pin_order DESC, created_at DESC")

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

	var posts []models.PostModel
	pag, err := pagination.Paginate(tx, q, &posts)
	return posts, pag, err
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

	post := models.PostModel{
		WriteBase: models.WriteBase{
			Title:  dto.Title,
			Text:   dto.Text,
			Images: dto.Images,
		},
		Slug:       dto.Slug,
		Summary:    dto.Summary,
		CategoryID: dto.CategoryID,
		Tags:       dto.Tags,
	}
	if dto.Copyright != nil {
		post.Copyright = *dto.Copyright
	} else {
		post.Copyright = true
	}
	if dto.IsPublished != nil {
		post.IsPublished = *dto.IsPublished
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
		updates["category_id"] = *dto.CategoryID
	}
	if dto.Copyright != nil {
		updates["copyright"] = *dto.Copyright
	}
	if dto.IsPublished != nil {
		updates["is_published"] = *dto.IsPublished
	}
	if dto.Tags != nil {
		updates["tags"] = dto.Tags
	}
	if dto.Pin != nil {
		updates["pin"] = *dto.Pin
	}
	if dto.PinOrder != nil {
		updates["pin_order"] = *dto.PinOrder
	}
	if dto.Images != nil {
		updates["images"] = dto.Images
	}

	if err := s.db.Model(post).Updates(updates).Error; err != nil {
		return nil, err
	}

	if oldSlug != "" && s.slugTracker != nil {
		go s.slugTracker.Track(oldSlug, "post", post.ID) // nolint:errcheck
	}
	return post, nil
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
