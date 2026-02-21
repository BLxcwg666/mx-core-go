package project

import (
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type CreateProjectDTO struct {
	Name        string   `json:"name"        binding:"required"`
	Description string   `json:"description"`
	PreviewURL  string   `json:"preview_url"`
	DocURL      string   `json:"doc_url"`
	ProjectURL  string   `json:"project_url"`
	Images      []string `json:"images"`
	Avatar      string   `json:"avatar"`
	Text        string   `json:"text"`
}

type UpdateProjectDTO struct {
	Name        *string  `json:"name"`
	Description *string  `json:"description"`
	PreviewURL  *string  `json:"preview_url"`
	DocURL      *string  `json:"doc_url"`
	ProjectURL  *string  `json:"project_url"`
	Images      []string `json:"images"`
	Avatar      *string  `json:"avatar"`
	Text        *string  `json:"text"`
}

type projectResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	PreviewURL  string    `json:"preview_url"`
	DocURL      string    `json:"doc_url"`
	ProjectURL  string    `json:"project_url"`
	Images      []string  `json:"images"`
	Avatar      string    `json:"avatar"`
	Text        string    `json:"text"`
	Created     time.Time `json:"created"`
	Modified    time.Time `json:"modified"`
}

func toResponse(p *models.ProjectModel) projectResponse {
	images := p.Images
	if images == nil {
		images = []string{}
	}
	return projectResponse{
		ID: p.ID, Name: p.Name, Description: p.Description,
		PreviewURL: p.PreviewURL, DocURL: p.DocURL, ProjectURL: p.ProjectURL,
		Images: images, Avatar: p.Avatar, Text: p.Text,
		Created: p.CreatedAt, Modified: p.UpdatedAt,
	}
}

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) List(q pagination.Query) ([]models.ProjectModel, response.Pagination, error) {
	tx := s.db.Model(&models.ProjectModel{}).Order("created_at DESC")
	var items []models.ProjectModel
	pag, err := pagination.Paginate(tx, q, &items)
	return items, pag, err
}

func (s *Service) ListAll() ([]models.ProjectModel, error) {
	var items []models.ProjectModel
	err := s.db.Order("created_at DESC").Find(&items).Error
	return items, err
}

func (s *Service) GetByID(id string) (*models.ProjectModel, error) {
	var p models.ProjectModel
	if err := s.db.First(&p, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (s *Service) Create(dto *CreateProjectDTO) (*models.ProjectModel, error) {
	var count int64
	s.db.Model(&models.ProjectModel{}).Where("name = ?", dto.Name).Count(&count)
	if count > 0 {
		return nil, fmt.Errorf("name already exists")
	}
	p := models.ProjectModel{
		Name: dto.Name, Description: dto.Description,
		PreviewURL: dto.PreviewURL, DocURL: dto.DocURL, ProjectURL: dto.ProjectURL,
		Images: dto.Images, Avatar: dto.Avatar, Text: dto.Text,
	}
	return &p, s.db.Create(&p).Error
}

func (s *Service) Update(id string, dto *UpdateProjectDTO) (*models.ProjectModel, error) {
	p, err := s.GetByID(id)
	if err != nil || p == nil {
		return p, err
	}
	updates := map[string]interface{}{}
	if dto.Name != nil {
		updates["name"] = *dto.Name
	}
	if dto.Description != nil {
		updates["description"] = *dto.Description
	}
	if dto.PreviewURL != nil {
		updates["preview_url"] = *dto.PreviewURL
	}
	if dto.DocURL != nil {
		updates["doc_url"] = *dto.DocURL
	}
	if dto.ProjectURL != nil {
		updates["project_url"] = *dto.ProjectURL
	}
	if dto.Images != nil {
		updates["images"] = dto.Images
	}
	if dto.Avatar != nil {
		updates["avatar"] = *dto.Avatar
	}
	if dto.Text != nil {
		updates["text"] = *dto.Text
	}
	return p, s.db.Model(p).Updates(updates).Error
}

func (s *Service) Delete(id string) error {
	return s.db.Delete(&models.ProjectModel{}, "id = ?", id).Error
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/projects")
	g.GET("", h.list)
	g.GET("/all", h.listAll)
	g.GET("/:id", h.get)

	a := g.Group("", authMW)
	a.POST("", h.create)
	a.PUT("/:id", h.update)
	a.DELETE("/:id", h.delete)
}

func (h *Handler) list(c *gin.Context) {
	q := pagination.FromContext(c)
	items, pag, err := h.svc.List(q)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	out := make([]projectResponse, len(items))
	for i, p := range items {
		out[i] = toResponse(&p)
	}
	response.Paged(c, out, pag)
}

func (h *Handler) listAll(c *gin.Context) {
	items, err := h.svc.ListAll()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	out := make([]projectResponse, len(items))
	for i, p := range items {
		out[i] = toResponse(&p)
	}
	response.OK(c, out)
}

func (h *Handler) get(c *gin.Context) {
	p, err := h.svc.GetByID(c.Param("id"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if p == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, toResponse(p))
}

func (h *Handler) create(c *gin.Context) {
	var dto CreateProjectDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	p, err := h.svc.Create(&dto)
	if err != nil {
		if err.Error() == "name already exists" {
			response.Conflict(c, err.Error())
			return
		}
		response.InternalError(c, err)
		return
	}
	response.Created(c, toResponse(p))
}

func (h *Handler) update(c *gin.Context) {
	var dto UpdateProjectDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	p, err := h.svc.Update(c.Param("id"), &dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if p == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, toResponse(p))
}

func (h *Handler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Param("id")); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}
