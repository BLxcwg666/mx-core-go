package recently

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type CreateRecentlyDTO struct {
	Content      string          `json:"content"       binding:"required"`
	RefType      *models.RefType `json:"ref_type"`
	RefID        *string         `json:"ref_id"`
	AllowComment *bool           `json:"allow_comment"`
}

type UpdateRecentlyDTO struct {
	Content      *string         `json:"content"`
	RefType      *models.RefType `json:"ref_type"`
	RefID        *string         `json:"ref_id"`
	AllowComment *bool           `json:"allow_comment"`
}

type recentlyResponse struct {
	ID           string          `json:"id"`
	Content      string          `json:"content"`
	RefType      *models.RefType `json:"ref_type"`
	RefID        *string         `json:"ref_id"`
	UpCount      int             `json:"up"`
	DownCount    int             `json:"down"`
	AllowComment bool            `json:"allow_comment"`
	Created      time.Time       `json:"created"`
	Modified     *time.Time      `json:"modified"`
}

func toResponse(r *models.RecentlyModel) recentlyResponse {
	var modified *time.Time
	if !r.UpdatedAt.IsZero() && r.UpdatedAt.Year() > 1 {
		modifiedAt := r.UpdatedAt
		modified = &modifiedAt
	}
	return recentlyResponse{
		ID: r.ID, Content: r.Content,
		RefType: r.RefType, RefID: r.RefID,
		UpCount: r.UpCount, DownCount: r.DownCount,
		AllowComment: r.AllowComment,
		Created:      r.CreatedAt, Modified: modified,
	}
}

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) List(q pagination.Query) ([]models.RecentlyModel, response.Pagination, error) {
	tx := s.db.Model(&models.RecentlyModel{}).Order("created_at DESC")
	var items []models.RecentlyModel
	pag, err := pagination.Paginate(tx, q, &items)
	return items, pag, err
}

func (s *Service) ListAll() ([]models.RecentlyModel, error) {
	var items []models.RecentlyModel
	return items, s.db.Order("created_at DESC").Find(&items).Error
}

func (s *Service) GetByID(id string) (*models.RecentlyModel, error) {
	var r models.RecentlyModel
	if err := s.db.First(&r, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

func (s *Service) GetLatest() (*models.RecentlyModel, error) {
	var r models.RecentlyModel
	if err := s.db.Order("created_at DESC").First(&r).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

func (s *Service) Create(dto *CreateRecentlyDTO) (*models.RecentlyModel, error) {
	r := models.RecentlyModel{
		Content: dto.Content, RefType: dto.RefType, RefID: dto.RefID,
		AllowComment: true,
	}
	if dto.AllowComment != nil {
		r.AllowComment = *dto.AllowComment
	}
	return &r, s.db.Create(&r).Error
}

func (s *Service) Delete(id string) error {
	return s.db.Delete(&models.RecentlyModel{}, "id = ?", id).Error
}

func (s *Service) Update(id string, dto *UpdateRecentlyDTO) (*models.RecentlyModel, error) {
	r, err := s.GetByID(id)
	if err != nil || r == nil {
		return r, err
	}
	updates := map[string]interface{}{}
	if dto.Content != nil {
		updates["content"] = *dto.Content
	}
	if dto.RefType != nil {
		updates["ref_type"] = *dto.RefType
	}
	if dto.RefID != nil {
		updates["ref_id"] = *dto.RefID
	}
	if dto.AllowComment != nil {
		updates["allow_comment"] = *dto.AllowComment
	}
	if len(updates) == 0 {
		return r, nil
	}
	return r, s.db.Model(r).Updates(updates).Error
}

func (s *Service) Vote(id string, up bool) error {
	col := "down_count"
	if up {
		col = "up_count"
	}
	return s.db.Model(&models.RecentlyModel{}).Where("id = ?", id).
		UpdateColumn(col, gorm.Expr(col+" + 1")).Error
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	for _, prefix := range []string{"/recently", "/shorthand"} {
		g := rg.Group(prefix)
		g.GET("", h.list)
		g.GET("/all", h.listAll)
		g.GET("/latest", h.latest)
		g.GET("/attitude/:id", h.attitude)
		g.GET("/:id", h.get)
		g.POST("/:id/up", h.voteUp)
		g.POST("/:id/down", h.voteDown)

		a := g.Group("", authMW)
		a.POST("", h.create)
		a.PUT("/:id", h.update)
		a.DELETE("/:id", h.delete)
	}
}

func (h *Handler) list(c *gin.Context) {
	q := pagination.FromContext(c)
	items, pag, err := h.svc.List(q)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	out := make([]recentlyResponse, len(items))
	for i, r := range items {
		out[i] = toResponse(&r)
	}
	response.Paged(c, out, pag)
}

func (h *Handler) get(c *gin.Context) {
	r, err := h.svc.GetByID(c.Param("id"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if r == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, toResponse(r))
}

func (h *Handler) listAll(c *gin.Context) {
	items, err := h.svc.ListAll()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	out := make([]recentlyResponse, len(items))
	for i, r := range items {
		out[i] = toResponse(&r)
	}
	response.OK(c, out)
}

func (h *Handler) latest(c *gin.Context) {
	r, err := h.svc.GetLatest()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if r == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, toResponse(r))
}

func (h *Handler) voteUp(c *gin.Context) {
	if err := h.svc.Vote(c.Param("id"), true); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) voteDown(c *gin.Context) {
	if err := h.svc.Vote(c.Param("id"), false); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) attitude(c *gin.Context) {
	switch c.Query("attitude") {
	case "up", "like":
		if err := h.svc.Vote(c.Param("id"), true); err != nil {
			response.InternalError(c, err)
			return
		}
		response.OK(c, gin.H{"code": 1})
	case "down", "hate":
		if err := h.svc.Vote(c.Param("id"), false); err != nil {
			response.InternalError(c, err)
			return
		}
		response.OK(c, gin.H{"code": 1})
	default:
		response.BadRequest(c, "attitude must be up|down")
	}
}

func (h *Handler) create(c *gin.Context) {
	var dto CreateRecentlyDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	r, err := h.svc.Create(&dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, toResponse(r))
}

func (h *Handler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Param("id")); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) update(c *gin.Context) {
	var dto UpdateRecentlyDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	r, err := h.svc.Update(c.Param("id"), &dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if r == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, toResponse(r))
}
