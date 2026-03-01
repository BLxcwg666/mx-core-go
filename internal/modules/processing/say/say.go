package say

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type CreateSayDTO struct {
	Text   string `json:"text"   binding:"required"`
	Source string `json:"source"`
	Author string `json:"author"`
}

type UpdateSayDTO struct {
	Text   *string `json:"text"`
	Source *string `json:"source"`
	Author *string `json:"author"`
}

type sayResponse struct {
	ID       string    `json:"id"`
	Text     string    `json:"text"`
	Source   string    `json:"source"`
	Author   string    `json:"author"`
	Created  time.Time `json:"created"`
	Modified time.Time `json:"modified"`
}

func toResponse(s *models.SayModel) sayResponse {
	return sayResponse{
		ID: s.ID, Text: s.Text, Source: s.Source, Author: s.Author,
		Created: s.CreatedAt, Modified: s.UpdatedAt,
	}
}

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) List(q pagination.Query) ([]models.SayModel, response.Pagination, error) {
	tx := s.db.Model(&models.SayModel{}).Order("created_at DESC")
	var items []models.SayModel
	pag, err := pagination.Paginate(tx, q, &items)
	return items, pag, err
}

func (s *Service) ListAll() ([]models.SayModel, error) {
	var items []models.SayModel
	err := s.db.Order("created_at DESC").Find(&items).Error
	return items, err
}

func (s *Service) Random() (*models.SayModel, error) {
	var item models.SayModel
	if err := s.db.Order("RAND()").First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

func (s *Service) GetByID(id string) (*models.SayModel, error) {
	var item models.SayModel
	if err := s.db.First(&item, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

func (s *Service) Create(dto *CreateSayDTO) (*models.SayModel, error) {
	item := models.SayModel{Text: dto.Text, Source: dto.Source, Author: dto.Author}
	return &item, s.db.Create(&item).Error
}

func (s *Service) Update(id string, dto *UpdateSayDTO) (*models.SayModel, error) {
	item, err := s.GetByID(id)
	if err != nil || item == nil {
		return item, err
	}
	updates := map[string]interface{}{}
	if dto.Text != nil {
		updates["text"] = *dto.Text
	}
	if dto.Source != nil {
		updates["source"] = *dto.Source
	}
	if dto.Author != nil {
		updates["author"] = *dto.Author
	}
	return item, s.db.Model(item).Updates(updates).Error
}

func (s *Service) Delete(id string) error {
	return s.db.Delete(&models.SayModel{}, "id = ?", id).Error
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/says")
	g.GET("", h.list)
	g.GET("/all", h.listAll)
	g.GET("/random", h.random)
	g.GET("/:id", h.get)

	a := g.Group("", authMW)
	a.POST("", h.create)
	a.PUT("/:id", h.update)
	a.PATCH("/:id", h.update)
	a.DELETE("/:id", h.delete)
}

func (h *Handler) list(c *gin.Context) {
	q := pagination.FromContext(c)
	items, pag, err := h.svc.List(q)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	out := make([]sayResponse, len(items))
	for i, s := range items {
		out[i] = toResponse(&s)
	}
	response.Paged(c, out, pag)
}

func (h *Handler) listAll(c *gin.Context) {
	items, err := h.svc.ListAll()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	out := make([]sayResponse, len(items))
	for i, s := range items {
		out[i] = toResponse(&s)
	}
	response.OK(c, out)
}

func (h *Handler) random(c *gin.Context) {
	item, err := h.svc.Random()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if item == nil {
		response.OK(c, gin.H{"data": nil})
		return
	}
	response.OK(c, gin.H{"data": toResponse(item)})
}

func (h *Handler) get(c *gin.Context) {
	item, err := h.svc.GetByID(c.Param("id"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if item == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, toResponse(item))
}

func (h *Handler) create(c *gin.Context) {
	var dto CreateSayDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	item, err := h.svc.Create(&dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, toResponse(item))
}

func (h *Handler) update(c *gin.Context) {
	var dto UpdateSayDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	item, err := h.svc.Update(c.Param("id"), &dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if item == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, toResponse(item))
}

func (h *Handler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Param("id")); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}
