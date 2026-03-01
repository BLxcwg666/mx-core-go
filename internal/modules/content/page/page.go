package page

import (
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/system/util/slugtracker"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type CreatePageDTO struct {
	Slug         string                 `json:"slug"          binding:"required"`
	Title        string                 `json:"title"         binding:"required"`
	Text         string                 `json:"text"          binding:"required"`
	Subtitle     string                 `json:"subtitle"`
	Order        *int                   `json:"order"`
	Meta         map[string]interface{} `json:"meta"`
	AllowComment *bool                  `json:"allowComment"`
	Images       []models.Image         `json:"images"`
}

type UpdatePageDTO struct {
	Slug         *string                `json:"slug"`
	Title        *string                `json:"title"`
	Text         *string                `json:"text"`
	Subtitle     *string                `json:"subtitle"`
	Order        *int                   `json:"order"`
	Meta         map[string]interface{} `json:"meta"`
	AllowComment *bool                  `json:"allowComment"`
	Images       []models.Image         `json:"images"`
}

type pageResponse struct {
	ID           string                 `json:"id"`
	Slug         string                 `json:"slug"`
	Title        string                 `json:"title"`
	Text         string                 `json:"text"`
	Subtitle     string                 `json:"subtitle"`
	Order        int                    `json:"order"`
	Meta         map[string]interface{} `json:"meta"`
	AllowComment bool                   `json:"allowComment"`
	Images       []models.Image         `json:"images"`
	Created      time.Time              `json:"created"`
	Modified     time.Time              `json:"modified"`
}

func toResponse(p *models.PageModel) pageResponse {
	images := p.Images
	if images == nil {
		images = []models.Image{}
	}
	return pageResponse{
		ID: p.ID, Slug: p.Slug, Title: p.Title, Text: p.Text,
		Subtitle: p.Subtitle, Order: p.Order, Meta: p.Meta,
		AllowComment: p.AllowComment, Images: images,
		Created: p.CreatedAt, Modified: p.UpdatedAt,
	}
}

type Service struct {
	db          *gorm.DB
	slugTracker *slugtracker.Service
}

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

// SetSlugTracker wires up slug change tracking (optional).
func (s *Service) SetSlugTracker(st *slugtracker.Service) { s.slugTracker = st }

func (s *Service) List(q pagination.Query) ([]models.PageModel, response.Pagination, error) {
	tx := s.db.Model(&models.PageModel{}).Order("order_num ASC, created_at ASC")
	var pages []models.PageModel
	pag, err := pagination.Paginate(tx, q, &pages)
	return pages, pag, err
}

func (s *Service) GetBySlug(slug string) (*models.PageModel, error) {
	var p models.PageModel
	if err := s.db.Where("slug = ?", slug).First(&p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (s *Service) GetByID(id string) (*models.PageModel, error) {
	var p models.PageModel
	if err := s.db.First(&p, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// GetByIdentifier fetches by ID first (admin compatibility), then slug fallback.
func (s *Service) GetByIdentifier(identifier string) (*models.PageModel, error) {
	if p, err := s.GetByID(identifier); err != nil {
		return nil, err
	} else if p != nil {
		return p, nil
	}
	return s.GetBySlug(identifier)
}

func (s *Service) Create(dto *CreatePageDTO) (*models.PageModel, error) {
	var count int64
	s.db.Model(&models.PageModel{}).Where("slug = ?", dto.Slug).Count(&count)
	if count > 0 {
		return nil, fmt.Errorf("slug already exists")
	}
	p := models.PageModel{
		WriteBase: models.WriteBase{Title: dto.Title, Text: dto.Text, Images: dto.Images},
		Slug:      dto.Slug, Subtitle: dto.Subtitle, Meta: dto.Meta,
	}
	if dto.Order != nil {
		p.Order = *dto.Order
	}
	if dto.AllowComment != nil {
		p.AllowComment = *dto.AllowComment
	} else {
		p.AllowComment = true
	}
	return &p, s.db.Create(&p).Error
}

func (s *Service) Update(id string, dto *UpdatePageDTO) (*models.PageModel, error) {
	p, err := s.GetByID(id)
	if err != nil || p == nil {
		return p, err
	}
	updates := map[string]interface{}{}
	var oldSlug string
	if dto.Slug != nil && *dto.Slug != p.Slug {
		oldSlug = p.Slug
		updates["slug"] = *dto.Slug
	}
	if dto.Title != nil {
		updates["title"] = *dto.Title
	}
	if dto.Text != nil {
		updates["text"] = *dto.Text
	}
	if dto.Subtitle != nil {
		updates["subtitle"] = *dto.Subtitle
	}
	if dto.Order != nil {
		updates["order_num"] = *dto.Order
	}
	if dto.Meta != nil {
		updates["meta"] = dto.Meta
	}
	if dto.AllowComment != nil {
		updates["allow_comment"] = *dto.AllowComment
	}
	if dto.Images != nil {
		updates["images"] = dto.Images
	}
	if err := s.db.Model(p).Updates(updates).Error; err != nil {
		return nil, err
	}
	if oldSlug != "" && s.slugTracker != nil {
		go s.slugTracker.Track(oldSlug, "page", p.ID) //nolint:errcheck
	}
	return p, nil
}

func (s *Service) Delete(id string) error {
	if s.slugTracker != nil {
		go s.slugTracker.DeleteByTargetID(id) //nolint:errcheck
	}
	return s.db.Delete(&models.PageModel{}, "id = ?", id).Error
}

func (s *Service) Reorder(id string, order int) {
	s.db.Model(&models.PageModel{}).Where("id = ?", id).Update("order_num", order)
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/pages")
	g.GET("", h.list)
	g.GET("/slug/:slug", h.getBySlug)
	g.GET("/:identifier", h.getByIdentifier)

	a := g.Group("", authMW)
	a.POST("", h.create)
	a.PATCH("/reorder", h.reorder)
	a.PUT("/:id", h.update)
	a.PATCH("/:id", h.update)
	a.DELETE("/:id", h.delete)
}

func (h *Handler) list(c *gin.Context) {
	q := pagination.FromContext(c)
	pages, pag, err := h.svc.List(q)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	items := make([]pageResponse, len(pages))
	for i, p := range pages {
		items[i] = toResponse(&p)
	}
	response.Paged(c, items, pag)
}

func (h *Handler) getBySlug(c *gin.Context) {
	p, err := h.svc.GetBySlug(c.Param("slug"))
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

func (h *Handler) getByIdentifier(c *gin.Context) {
	p, err := h.svc.GetByIdentifier(c.Param("identifier"))
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
	var dto CreatePageDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	p, err := h.svc.Create(&dto)
	if err != nil {
		if err.Error() == "slug already exists" {
			response.Conflict(c, err.Error())
			return
		}
		response.InternalError(c, err)
		return
	}
	response.Created(c, toResponse(p))
}

func (h *Handler) update(c *gin.Context) {
	var dto UpdatePageDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	id := c.Param("id")
	isAdmin := middleware.IsAuthenticated(c)
	_ = isAdmin

	p, err := h.svc.Update(id, &dto)
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

type reorderDTO struct {
	IDs []string `json:"ids" binding:"required"`
}

// PATCH /pages/reorder — reorder pages by setting Order field
func (h *Handler) reorder(c *gin.Context) {
	var dto reorderDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	for i, id := range dto.IDs {
		h.svc.Reorder(id, i)
	}
	response.NoContent(c)
}
