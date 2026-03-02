package category

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mx-space/core/internal/models"
	"gorm.io/gorm"
)

const (
	CategoryTypeCategory = 0
	CategoryTypeTag      = 1
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

type CategoryListItem struct {
	ID       string    `json:"id"`
	Type     int       `json:"type"`
	Count    int64     `json:"count"`
	Name     string    `json:"name"`
	Slug     string    `json:"slug"`
	Created  time.Time `json:"created"`
	Modified time.Time `json:"modified"`
}

type TagSummary struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type CategorySlim struct {
	ID   string `json:"id"`
	Type int    `json:"type"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type TagPostLite struct {
	ID       string       `json:"id"`
	Title    string       `json:"title"`
	Slug     string       `json:"slug"`
	Category CategorySlim `json:"category"`
	Created  time.Time    `json:"created"`
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

func (s *Service) ListCategories() ([]CategoryListItem, error) {
	var cats []models.CategoryModel
	if err := s.db.
		Where("type = ?", CategoryTypeCategory).
		Order("created_at ASC").
		Find(&cats).Error; err != nil {
		return nil, err
	}

	countByCategory, err := s.countPublishedPostsByCategory()
	if err != nil {
		return nil, err
	}

	items := make([]CategoryListItem, 0, len(cats))
	for _, cat := range cats {
		items = append(items, CategoryListItem{
			ID:       cat.ID,
			Type:     cat.Type,
			Count:    countByCategory[cat.ID],
			Name:     cat.Name,
			Slug:     cat.Slug,
			Created:  cat.CreatedAt,
			Modified: cat.UpdatedAt,
		})
	}
	return items, nil
}

func (s *Service) ListTags() ([]TagSummary, error) {
	type postTagsRow struct {
		TagsRaw []byte `gorm:"column:tags"`
	}

	var rows []postTagsRow
	if err := s.db.
		Model(&models.PostModel{}).
		Select("tags").
		Where("is_published = ?", true).
		Find(&rows).Error; err != nil {
		return nil, err
	}

	countMap := make(map[string]int)
	for _, row := range rows {
		if len(row.TagsRaw) == 0 {
			continue
		}

		var tags []string
		if err := json.Unmarshal(row.TagsRaw, &tags); err != nil {
			continue
		}

		for _, tag := range tags {
			name := strings.TrimSpace(tag)
			if name == "" {
				continue
			}
			countMap[name]++
		}
	}

	items := make([]TagSummary, 0, len(countMap))
	for name, count := range countMap {
		items = append(items, TagSummary{
			Name:  name,
			Count: count,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].Name < items[j].Name
		}
		return items[i].Count > items[j].Count
	})

	return items, nil
}

func (s *Service) countPublishedPostsByCategory() (map[string]int64, error) {
	type countRow struct {
		CategoryID string `gorm:"column:category_id"`
		Count      int64  `gorm:"column:count"`
	}

	var rows []countRow
	if err := s.db.
		Model(&models.PostModel{}).
		Select("category_id, COUNT(*) AS count").
		Where("is_published = ? AND category_id IS NOT NULL", true).
		Group("category_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	countByCategory := make(map[string]int64, len(rows))
	for _, row := range rows {
		if row.CategoryID == "" {
			continue
		}
		countByCategory[row.CategoryID] = row.Count
	}
	return countByCategory, nil
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
	if isLikelyID(query) {
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

func (s *Service) ListPostsByTag(tag string) ([]TagPostLite, error) {
	name := strings.TrimSpace(tag)
	if name == "" {
		return nil, nil
	}

	type row struct {
		ID           string    `gorm:"column:id"`
		Title        string    `gorm:"column:title"`
		Slug         string    `gorm:"column:slug"`
		Created      time.Time `gorm:"column:created"`
		CategoryID   string    `gorm:"column:category_id"`
		CategoryType int       `gorm:"column:category_type"`
		CategoryName string    `gorm:"column:category_name"`
		CategorySlug string    `gorm:"column:category_slug"`
	}

	var rows []row
	err := s.db.
		Table("posts").
		Select(
			`posts.id,
			 posts.title,
			 posts.slug,
			 posts.created_at AS created,
			 categories.id AS category_id,
			 categories.type AS category_type,
			 categories.name AS category_name,
			 categories.slug AS category_slug`,
		).
		Joins("JOIN categories ON categories.id = posts.category_id").
		Where("posts.is_published = ?", true).
		Where("JSON_CONTAINS(posts.tags, ?)", fmt.Sprintf("%q", name)).
		Order("posts.created_at DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	items := make([]TagPostLite, 0, len(rows))
	for _, row := range rows {
		items = append(items, TagPostLite{
			ID:      row.ID,
			Title:   row.Title,
			Slug:    row.Slug,
			Created: row.Created,
			Category: CategorySlim{
				ID:   row.CategoryID,
				Type: row.CategoryType,
				Name: row.CategoryName,
				Slug: row.CategorySlug,
			},
		})
	}
	return items, nil
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

func isLikelyID(value string) bool {
	if _, err := uuid.Parse(value); err == nil {
		return true
	}
	if len(value) == 24 {
		_, err := hex.DecodeString(value)
		return err == nil
	}
	return false
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
