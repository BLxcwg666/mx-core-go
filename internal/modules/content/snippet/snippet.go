package snippet

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type CreateSnippetDTO struct {
	Type      models.SnippetType `json:"type"      binding:"required"`
	Name      string             `json:"name"      binding:"required"`
	Reference string             `json:"reference" binding:"required"`
	Raw       string             `json:"raw"       binding:"required"`
	Comment   string             `json:"comment"`
	Private   *bool              `json:"private"`
	Enable    *bool              `json:"enable"`
	Schema    string             `json:"schema"`
	Metatype  string             `json:"metatype"`
	Method    string             `json:"method"`
}

type UpdateSnippetDTO struct {
	Type      *models.SnippetType `json:"type"`
	Name      *string             `json:"name"`
	Reference *string             `json:"reference"`
	Raw       *string             `json:"raw"`
	Comment   *string             `json:"comment"`
	Private   *bool               `json:"private"`
	Enable    *bool               `json:"enable"`
	Schema    *string             `json:"schema"`
	Metatype  *string             `json:"metatype"`
	Method    *string             `json:"method"`
}

type snippetResponse struct {
	ID        string             `json:"id"`
	Type      models.SnippetType `json:"type"`
	Name      string             `json:"name"`
	Reference string             `json:"reference"`
	Raw       string             `json:"raw"`
	Comment   string             `json:"comment"`
	Private   bool               `json:"private"`
	Enable    bool               `json:"enable"`
	Schema    string             `json:"schema"`
	Metatype  string             `json:"metatype"`
	Method    string             `json:"method"`
	BuiltIn   bool               `json:"built_in"`
	Created   time.Time          `json:"created"`
	Updated   time.Time          `json:"updated"`
}

func toResponse(s *models.SnippetModel) snippetResponse {
	return snippetResponse{
		ID: s.ID, Type: normalizeSnippetType(s.Type), Name: s.Name, Reference: s.Reference,
		Raw: s.Raw, Comment: s.Comment, Private: s.Private, Enable: s.Enable,
		Schema: s.Schema, Metatype: s.Metatype, Method: s.Method, BuiltIn: s.BuiltIn,
		Created: s.CreatedAt, Updated: s.UpdatedAt,
	}
}

func normalizeSnippetType(t models.SnippetType) models.SnippetType {
	switch strings.ToLower(strings.TrimSpace(string(t))) {
	case string(models.SnippetTypeJSON):
		return models.SnippetTypeJSON
	case string(models.SnippetTypeJSON5):
		return models.SnippetTypeJSON5
	case string(models.SnippetTypeFunction):
		return models.SnippetTypeFunction
	case string(models.SnippetTypeYAML):
		return models.SnippetTypeYAML
	case "html", "javascript", "css", "sql", "typescript", string(models.SnippetTypeText):
		return models.SnippetTypeText
	default:
		if t == "" {
			return models.SnippetTypeJSON
		}
		return models.SnippetTypeText
	}
}

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) List(q pagination.Query, private bool) ([]models.SnippetModel, response.Pagination, error) {
	tx := s.db.Model(&models.SnippetModel{}).Order("reference ASC, name ASC")
	if !private {
		tx = tx.Where("private = ?", false)
	}
	var items []models.SnippetModel
	pag, err := pagination.Paginate(tx, q, &items)
	return items, pag, err
}

func (s *Service) GetByReferenceAndName(reference, name string) (*models.SnippetModel, error) {
	var item models.SnippetModel
	if err := s.db.Where("reference = ? AND name = ?", reference, name).First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

func (s *Service) GetByID(id string) (*models.SnippetModel, error) {
	var item models.SnippetModel
	if err := s.db.First(&item, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

func (s *Service) Create(dto *CreateSnippetDTO) (*models.SnippetModel, error) {
	dto.Type = normalizeSnippetType(dto.Type)
	var count int64
	s.db.Model(&models.SnippetModel{}).Where("reference = ? AND name = ?", dto.Reference, dto.Name).Count(&count)
	if count > 0 {
		return nil, fmt.Errorf("snippet already exists")
	}

	item := models.SnippetModel{
		Type: dto.Type, Name: dto.Name, Reference: dto.Reference,
		Raw: dto.Raw, Comment: dto.Comment, Schema: dto.Schema,
		Metatype: dto.Metatype, Method: dto.Method,
		Enable: true, Private: false,
	}
	if dto.Private != nil {
		item.Private = *dto.Private
	}
	if dto.Enable != nil {
		item.Enable = *dto.Enable
	}
	return &item, s.db.Create(&item).Error
}

func (s *Service) Update(id string, dto *UpdateSnippetDTO) (*models.SnippetModel, error) {
	item, err := s.GetByID(id)
	if err != nil || item == nil {
		return item, err
	}
	updates := map[string]interface{}{}
	if dto.Type != nil {
		updates["type"] = normalizeSnippetType(*dto.Type)
	}
	if dto.Name != nil {
		updates["name"] = *dto.Name
	}
	if dto.Reference != nil {
		updates["reference"] = *dto.Reference
	}
	if dto.Raw != nil {
		updates["raw"] = *dto.Raw
	}
	if dto.Comment != nil {
		updates["comment"] = *dto.Comment
	}
	if dto.Private != nil {
		updates["private"] = *dto.Private
	}
	if dto.Enable != nil {
		updates["enable"] = *dto.Enable
	}
	if dto.Schema != nil {
		updates["schema"] = *dto.Schema
	}
	if dto.Metatype != nil {
		updates["metatype"] = *dto.Metatype
	}
	if dto.Method != nil {
		updates["method"] = *dto.Method
	}
	return item, s.db.Model(item).Updates(updates).Error
}

func (s *Service) Delete(id string) error {
	return s.db.Delete(&models.SnippetModel{}, "id = ?", id).Error
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/snippets")
	g.GET("/:id/:name", h.getByRef)

	a := g.Group("", authMW)
	a.GET("", h.list)
	a.GET("/group", h.listGroup)
	a.GET("/group/", h.listGroupByEmptyReference)
	a.GET("/group/:reference", h.listGroupByReference)
	a.GET("/all", h.listAll)
	a.GET("/:id", h.getByID)
	a.POST("", h.create)
	a.POST("/import", h.importSnippets)
	a.PUT("/:id", h.update)
	a.PATCH("/:id", h.update) // legacy compatibility
	a.DELETE("/:id", h.delete)
}

func (h *Handler) list(c *gin.Context) {
	q := pagination.FromContext(c)
	items, pag, err := h.svc.List(q, true)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	out := make([]snippetResponse, len(items))
	for i, s := range items {
		out[i] = toResponse(&s)
	}
	response.Paged(c, out, pag)
}

func (h *Handler) listAll(c *gin.Context) {
	q := pagination.FromContext(c)
	items, pag, err := h.svc.List(q, true)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	out := make([]snippetResponse, len(items))
	for i, s := range items {
		out[i] = toResponse(&s)
	}
	response.Paged(c, out, pag)
}

func (h *Handler) getByRef(c *gin.Context) {
	reference := c.Param("reference")
	if reference == "" {
		reference = c.Param("id")
	}
	item, err := h.svc.GetByReferenceAndName(reference, c.Param("name"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if item == nil {
		response.NotFoundMsg(c, "Snippet 不存在")
		return
	}
	if item.Private {
		response.ForbiddenMsg(c, "Snippet 是私有的")
		return
	}
	response.OK(c, toResponse(item))
}

func (h *Handler) create(c *gin.Context) {
	var dto CreateSnippetDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	item, err := h.svc.Create(&dto)
	if err != nil {
		if err.Error() == "snippet already exists" {
			response.Conflict(c, "Snippet 已存在")
			return
		}
		response.InternalError(c, err)
		return
	}
	response.Created(c, toResponse(item))
}

func (h *Handler) getByID(c *gin.Context) {
	item, err := h.svc.GetByID(c.Param("id"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if item == nil {
		response.NotFoundMsg(c, "Snippet 不存在")
		return
	}
	response.OK(c, toResponse(item))
}

func (h *Handler) update(c *gin.Context) {
	var dto UpdateSnippetDTO
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
		response.NotFoundMsg(c, "Snippet 不存在")
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

// GET /snippets/group — return snippets grouped by reference
func (h *Handler) listGroup(c *gin.Context) {
	q := pagination.FromContext(c)
	type snippetGroup struct {
		Reference string `json:"reference"`
		Count     int64  `json:"count"`
	}

	var total int64
	if err := h.svc.db.Model(&models.SnippetModel{}).Distinct("reference").Count(&total).Error; err != nil {
		response.InternalError(c, err)
		return
	}

	var groups []snippetGroup
	offset := (q.Page - 1) * q.Size
	if err := h.svc.db.Model(&models.SnippetModel{}).
		Select("reference, COUNT(*) AS count").
		Group("reference").
		Order("reference ASC").
		Offset(offset).
		Limit(q.Size).
		Find(&groups).Error; err != nil {
		response.InternalError(c, err)
		return
	}

	totalPage := int((total + int64(q.Size) - 1) / int64(q.Size))
	pag := response.Pagination{
		Total:       total,
		CurrentPage: q.Page,
		TotalPage:   totalPage,
		Size:        q.Size,
		HasNextPage: q.Page < totalPage,
	}
	response.Paged(c, groups, pag)
}

func (h *Handler) listGroupByReference(c *gin.Context) {
	h.listGroupByReferenceWithValue(c, c.Param("reference"))
}

func (h *Handler) listGroupByEmptyReference(c *gin.Context) {
	h.listGroupByReferenceWithValue(c, "")
}

func (h *Handler) listGroupByReferenceWithValue(c *gin.Context, ref string) {
	var items []models.SnippetModel
	h.svc.db.Where("reference = ?", ref).Order("name ASC").Find(&items)
	out := make([]snippetResponse, len(items))
	for i, s := range items {
		out[i] = toResponse(&s)
	}
	response.OK(c, out)
}

// POST /snippets/import — bulk import snippets
type importSnippetItem struct {
	Name      string             `json:"name"      binding:"required"`
	Reference string             `json:"reference" binding:"required"`
	Raw       string             `json:"raw"`
	Private   bool               `json:"private"`
	Type      models.SnippetType `json:"type"`
	Comment   string             `json:"comment"`
	Enable    *bool              `json:"enable"`
}

type importSnippetsDTO struct {
	Snippets []importSnippetItem `json:"snippets" binding:"required"`
	Packages []string            `json:"packages"` // Node.js packages — ignored in Go
}

func (h *Handler) importSnippets(c *gin.Context) {
	var dto importSnippetsDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	for _, item := range dto.Snippets {
		var count int64
		h.svc.db.Model(&models.SnippetModel{}).
			Where("name = ? AND reference = ?", item.Name, item.Reference).
			Count(&count)
		if count > 0 {
			continue
		}
		snippetType := normalizeSnippetType(item.Type)
		enable := true
		if item.Enable != nil {
			enable = *item.Enable
		}
		s := models.SnippetModel{
			Name:      item.Name,
			Reference: item.Reference,
			Raw:       item.Raw,
			Private:   item.Private,
			Type:      snippetType,
			Comment:   item.Comment,
			Enable:    enable,
		}
		h.svc.db.Create(&s)
	}
	response.OK(c, "OK")
}
