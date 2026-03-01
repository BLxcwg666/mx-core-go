package topic

import (
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type CreateTopicDTO struct {
	Name        string `json:"name"        binding:"required"`
	Slug        string `json:"slug"        binding:"required"`
	Description string `json:"description"`
	Introduce   string `json:"introduce"`
	Icon        string `json:"icon"`
}

type UpdateTopicDTO struct {
	Name        *string `json:"name"`
	Slug        *string `json:"slug"`
	Description *string `json:"description"`
	Introduce   *string `json:"introduce"`
	Icon        *string `json:"icon"`
}

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) List() ([]models.TopicModel, error) {
	var topics []models.TopicModel
	return topics, s.db.Order("created_at ASC").Find(&topics).Error
}

func (s *Service) GetByID(id string) (*models.TopicModel, error) {
	var t models.TopicModel
	if err := s.db.First(&t, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (s *Service) GetBySlug(slug string) (*models.TopicModel, error) {
	var t models.TopicModel
	if err := s.db.Where("slug = ?", slug).First(&t).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (s *Service) Create(dto *CreateTopicDTO) (*models.TopicModel, error) {
	var count int64
	s.db.Model(&models.TopicModel{}).Where("slug = ? OR name = ?", dto.Slug, dto.Name).Count(&count)
	if count > 0 {
		return nil, fmt.Errorf("name or slug already exists")
	}
	t := models.TopicModel{
		Name: dto.Name, Slug: dto.Slug,
		Description: dto.Description, Introduce: dto.Introduce, Icon: dto.Icon,
	}
	return &t, s.db.Create(&t).Error
}

func (s *Service) Update(id string, dto *UpdateTopicDTO) (*models.TopicModel, error) {
	t, err := s.GetByID(id)
	if err != nil || t == nil {
		return t, err
	}
	updates := map[string]interface{}{}
	if dto.Name != nil {
		updates["name"] = *dto.Name
	}
	if dto.Slug != nil {
		updates["slug"] = *dto.Slug
	}
	if dto.Description != nil {
		updates["description"] = *dto.Description
	}
	if dto.Introduce != nil {
		updates["introduce"] = *dto.Introduce
	}
	if dto.Icon != nil {
		updates["icon"] = *dto.Icon
	}
	return t, s.db.Model(t).Updates(updates).Error
}

func (s *Service) Delete(id string) error {
	s.db.Model(&models.NoteModel{}).Where("topic_id = ?", id).Update("topic_id", nil)
	return s.db.Delete(&models.TopicModel{}, "id = ?", id).Error
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	t := rg.Group("/topics")
	t.GET("", h.list)
	t.GET("/all", h.listAll)
	t.GET("/slug/:slug", h.getBySlug)
	t.GET("/:id", h.get)

	a := t.Group("", authMW)
	a.POST("", h.create)
	a.PUT("/:id", h.update)
	a.PATCH("/:id", h.update) // legacy compatibility
	a.DELETE("/:id", h.delete)
}

func (h *Handler) list(c *gin.Context) {
	topics, err := h.svc.List()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, gin.H{"data": topics})
}

func (h *Handler) listAll(c *gin.Context) {
	topics, err := h.svc.List()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, topics)
}

func (h *Handler) get(c *gin.Context) {
	t, err := h.svc.GetByID(c.Param("id"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if t == nil {
		response.NotFoundMsg(c, "主题不存在")
		return
	}
	response.OK(c, t)
}

func (h *Handler) getBySlug(c *gin.Context) {
	t, err := h.svc.GetBySlug(c.Param("slug"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if t == nil {
		response.NotFoundMsg(c, "主题不存在")
		return
	}
	response.OK(c, t)
}

func (h *Handler) create(c *gin.Context) {
	var dto CreateTopicDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	t, err := h.svc.Create(&dto)
	if err != nil {
		if err.Error() == "name or slug already exists" {
			response.Conflict(c, err.Error())
			return
		}
		response.InternalError(c, err)
		return
	}
	response.Created(c, t)
}

func (h *Handler) update(c *gin.Context) {
	var dto UpdateTopicDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	t, err := h.svc.Update(c.Param("id"), &dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if t == nil {
		response.NotFoundMsg(c, "主题不存在")
		return
	}
	response.OK(c, t)
}

func (h *Handler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Param("id")); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}
