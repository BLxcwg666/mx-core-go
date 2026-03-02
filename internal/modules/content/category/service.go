package category

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mx-space/core/internal/models"
	"gorm.io/gorm"
)

type CreateCategoryDTO struct {
	Name string `json:"name" binding:"required"`
	Slug string `json:"slug" binding:"required"`
	Type *int   `json:"type"`
}

type UpdateCategoryDTO struct {
	Name *string `json:"name"`
	Slug *string `json:"slug"`
	Type *int    `json:"type"`
}

type Service struct {
	db *gorm.DB
}

type CategoryPostLite struct {
	ID       string    `json:"id"       gorm:"column:id"`
	Title    string    `json:"title"    gorm:"column:title"`
	Slug     string    `json:"slug"     gorm:"column:slug"`
	Created  time.Time `json:"created"  gorm:"column:created"`
	Modified time.Time `json:"modified" gorm:"column:modified"`
}

type CategoryDetail struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Slug     string             `json:"slug"`
	Type     int                `json:"type"`
	Count    int                `json:"count"`
	Created  time.Time          `json:"created"`
	Modified time.Time          `json:"modified"`
	Children []CategoryPostLite `json:"children"`
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) List() ([]models.CategoryModel, error) {
	var cats []models.CategoryModel
	return cats, s.db.Order("created_at ASC").Find(&cats).Error
}

func (s *Service) GetByID(id string) (*models.CategoryModel, error) {
	var cat models.CategoryModel
	if err := s.db.First(&cat, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &cat, nil
}

func (s *Service) GetByQuery(query string) (*models.CategoryModel, error) {
	if isUUID(query) {
		if cat, err := s.GetByID(query); err != nil {
			return nil, err
		} else if cat != nil {
			return cat, nil
		}
	}

	var cat models.CategoryModel
	if err := s.db.Where("slug = ? OR name = ?", query, query).First(&cat).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &cat, nil
}

func (s *Service) GetDetailByQuery(query string) (*CategoryDetail, error) {
	cat, err := s.GetByQuery(query)
	if err != nil || cat == nil {
		return nil, err
	}

	children, err := s.listPostsByCategory(cat.ID)
	if err != nil {
		return nil, err
	}
	if children == nil {
		children = []CategoryPostLite{}
	}

	return &CategoryDetail{
		ID:       cat.ID,
		Name:     cat.Name,
		Slug:     cat.Slug,
		Type:     cat.Type,
		Count:    len(children),
		Created:  cat.CreatedAt,
		Modified: cat.UpdatedAt,
		Children: children,
	}, nil
}

func (s *Service) listPostsByCategory(categoryID string) ([]CategoryPostLite, error) {
	var items []CategoryPostLite
	err := s.db.Model(&models.PostModel{}).
		Select("id, title, slug, created_at AS created, updated_at AS modified").
		Where("category_id = ? AND is_published = ?", categoryID, true).
		Order("created_at DESC").
		Find(&items).Error
	return items, err
}

func isUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func (s *Service) Create(dto *CreateCategoryDTO) (*models.CategoryModel, error) {
	var count int64
	s.db.Model(&models.CategoryModel{}).Where("slug = ? OR name = ?", dto.Slug, dto.Name).Count(&count)
	if count > 0 {
		return nil, fmt.Errorf("name or slug already exists")
	}

	cat := models.CategoryModel{Name: dto.Name, Slug: dto.Slug}
	if dto.Type != nil {
		cat.Type = *dto.Type
	}
	return &cat, s.db.Create(&cat).Error
}

func (s *Service) Update(id string, dto *UpdateCategoryDTO) (*models.CategoryModel, error) {
	cat, err := s.GetByID(id)
	if err != nil || cat == nil {
		return cat, err
	}
	updates := map[string]interface{}{}
	if dto.Name != nil {
		updates["name"] = *dto.Name
	}
	if dto.Slug != nil {
		updates["slug"] = *dto.Slug
	}
	if dto.Type != nil {
		updates["type"] = *dto.Type
	}
	return cat, s.db.Model(cat).Updates(updates).Error
}

func (s *Service) Delete(id string) error {
	s.db.Model(&models.PostModel{}).Where("category_id = ?", id).Update("category_id", nil)
	return s.db.Delete(&models.CategoryModel{}, "id = ?", id).Error
}
