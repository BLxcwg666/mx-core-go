package category

import (
	"errors"
	"fmt"

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
	if cat, err := s.GetByID(query); err != nil {
		return nil, err
	} else if cat != nil {
		return cat, nil
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
