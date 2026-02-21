package draft

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type CreateDraftDTO struct {
	RefType          models.DraftRefType    `json:"ref_type" binding:"required"`
	RefID            *string                `json:"ref_id"`
	Title            string                 `json:"title"`
	Text             string                 `json:"text"`
	Images           []models.Image         `json:"images"`
	Meta             map[string]interface{} `json:"meta"`
	TypeSpecificData map[string]interface{} `json:"type_specific_data"`
}

func (d *CreateDraftDTO) UnmarshalJSON(data []byte) error {
	type snakeCase CreateDraftDTO
	type camelCase struct {
		RefType          models.DraftRefType    `json:"refType"`
		RefID            *string                `json:"refId"`
		TypeSpecificData map[string]interface{} `json:"typeSpecificData"`
	}

	var snake snakeCase
	if err := json.Unmarshal(data, &snake); err != nil {
		return err
	}

	var camel camelCase
	if err := json.Unmarshal(data, &camel); err != nil {
		return err
	}

	*d = CreateDraftDTO(snake)
	if d.RefType == "" {
		d.RefType = camel.RefType
	}
	if d.RefID == nil {
		d.RefID = camel.RefID
	}
	if d.TypeSpecificData == nil {
		d.TypeSpecificData = camel.TypeSpecificData
	}

	return nil
}

type UpdateDraftDTO struct {
	Title            *string                `json:"title"`
	Text             *string                `json:"text"`
	Images           []models.Image         `json:"images"`
	Meta             map[string]interface{} `json:"meta"`
	TypeSpecificData map[string]interface{} `json:"type_specific_data"`
}

func (d *UpdateDraftDTO) UnmarshalJSON(data []byte) error {
	type snakeCase UpdateDraftDTO
	type camelCase struct {
		TypeSpecificData map[string]interface{} `json:"typeSpecificData"`
	}

	var snake snakeCase
	if err := json.Unmarshal(data, &snake); err != nil {
		return err
	}

	var camel camelCase
	if err := json.Unmarshal(data, &camel); err != nil {
		return err
	}

	*d = UpdateDraftDTO(snake)
	if d.TypeSpecificData == nil {
		d.TypeSpecificData = camel.TypeSpecificData
	}

	return nil
}

type draftResponse struct {
	ID               string                 `json:"id"`
	RefType          models.DraftRefType    `json:"ref_type"`
	RefID            *string                `json:"ref_id"`
	Title            string                 `json:"title"`
	Text             string                 `json:"text"`
	Images           []models.Image         `json:"images"`
	Meta             map[string]interface{} `json:"meta"`
	TypeSpecificData map[string]interface{} `json:"type_specific_data,omitempty"`
	Version          int                    `json:"version"`
	PublishedVersion *int                   `json:"published_version"`
	HistoryCount     int                    `json:"history_count"`
	Created          time.Time              `json:"created"`
	Modified         time.Time              `json:"modified"`
}

func toResponse(d *models.DraftModel) draftResponse {
	images := d.Images
	if images == nil {
		images = []models.Image{}
	}
	return draftResponse{
		ID: d.ID, RefType: d.RefType, RefID: d.RefID,
		Title: d.Title, Text: d.Text, Images: images, Meta: d.Meta,
		TypeSpecificData: d.TypeSpecificData,
		Version:          d.Version, PublishedVersion: d.PublishedVersion,
		HistoryCount: len(d.History),
		Created:      d.CreatedAt, Modified: d.UpdatedAt,
	}
}

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (s *Service) List(q pagination.Query, refType *string) ([]models.DraftModel, response.Pagination, error) {
	tx := s.db.Model(&models.DraftModel{}).Order("updated_at DESC")
	if refType != nil {
		tx = tx.Where("ref_type = ?", *refType)
	}
	var items []models.DraftModel
	pag, err := pagination.Paginate(tx, q, &items)
	return items, pag, err
}

func (s *Service) GetByID(id string) (*models.DraftModel, error) {
	var d models.DraftModel
	if err := s.db.Preload("History").First(&d, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

func (s *Service) GetByRef(refType, refID string) (*models.DraftModel, error) {
	var d models.DraftModel
	err := s.db.Where("ref_type = ? AND ref_id = ?", refType, refID).
		Preload("History").Order("version DESC").First(&d).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

func (s *Service) GetNewByRefType(refType string) ([]models.DraftModel, error) {
	tx := s.db.Model(&models.DraftModel{}).
		Where("ref_type = ? AND ref_id IS NULL", refType).
		Order("updated_at DESC")
	var items []models.DraftModel
	return items, tx.Find(&items).Error
}

func (s *Service) Create(dto *CreateDraftDTO) (*models.DraftModel, error) {
	d := models.DraftModel{
		RefType:          dto.RefType,
		RefID:            dto.RefID,
		Title:            dto.Title,
		Text:             dto.Text,
		Images:           dto.Images,
		Meta:             dto.Meta,
		TypeSpecificData: dto.TypeSpecificData,
		Version:          1,
	}
	return &d, s.db.Create(&d).Error
}

// Update saves a new version snapshot of the draft.
func (s *Service) Update(id string, dto *UpdateDraftDTO) (*models.DraftModel, error) {
	d, err := s.GetByID(id)
	if err != nil || d == nil {
		return d, err
	}

	history := models.DraftHistoryModel{
		DraftID:          d.ID,
		Version:          d.Version,
		Title:            d.Title,
		Text:             d.Text,
		TypeSpecificData: d.TypeSpecificData,
		SavedAt:          time.Now(),
		IsFullSnapshot:   true,
	}
	s.db.Create(&history)

	updates := map[string]interface{}{"version": d.Version + 1}
	if dto.Title != nil {
		updates["title"] = *dto.Title
	}
	if dto.Text != nil {
		updates["text"] = *dto.Text
	}
	if dto.Images != nil {
		updates["images"] = dto.Images
	}
	if dto.Meta != nil {
		updates["meta"] = dto.Meta
	}
	if dto.TypeSpecificData != nil {
		updates["type_specific_data"] = dto.TypeSpecificData
	}

	return d, s.db.Model(d).Updates(updates).Error
}

// Publish copies the draft content to the referenced article, or creates a new one.
func (s *Service) Publish(id string) (string, error) {
	d, err := s.GetByID(id)
	if err != nil || d == nil {
		return "", fmt.Errorf("draft not found")
	}

	var targetID string

	switch d.RefType {
	case models.DraftRefPost:
		if d.RefID != nil {
			updates := map[string]interface{}{"title": d.Title, "text": d.Text}
			if d.Images != nil {
				updates["images"] = d.Images
			}
			if err := s.db.Model(&models.PostModel{}).Where("id = ?", *d.RefID).Updates(updates).Error; err != nil {
				return "", err
			}
			targetID = *d.RefID
		} else {
			return "", fmt.Errorf("draft has no linked post; create a post first then attach draft")
		}

	case models.DraftRefNote:
		if d.RefID != nil {
			updates := map[string]interface{}{"title": d.Title, "text": d.Text}
			if err := s.db.Model(&models.NoteModel{}).Where("id = ?", *d.RefID).Updates(updates).Error; err != nil {
				return "", err
			}
			targetID = *d.RefID
		} else {
			return "", fmt.Errorf("draft has no linked note")
		}

	case models.DraftRefPage:
		if d.RefID != nil {
			updates := map[string]interface{}{"title": d.Title, "text": d.Text}
			if err := s.db.Model(&models.PageModel{}).Where("id = ?", *d.RefID).Updates(updates).Error; err != nil {
				return "", err
			}
			targetID = *d.RefID
		} else {
			return "", fmt.Errorf("draft has no linked page")
		}

	default:
		return "", fmt.Errorf("unknown ref_type: %s", d.RefType)
	}

	s.db.Model(d).Update("published_version", d.Version)

	return targetID, nil
}

func (s *Service) Delete(id string) error {
	s.db.Where("draft_id = ?", id).Delete(&models.DraftHistoryModel{})
	return s.db.Delete(&models.DraftModel{}, "id = ?", id).Error
}

// GetHistory returns all snapshots for a draft ordered by version desc.
func (s *Service) GetHistory(draftID string) ([]models.DraftHistoryModel, error) {
	var history []models.DraftHistoryModel
	err := s.db.Where("draft_id = ?", draftID).
		Order("version DESC").Find(&history).Error
	return history, err
}

// RestoreVersion restores the draft to a specific historical version.
func (s *Service) RestoreVersion(draftID string, version int) (*models.DraftModel, error) {
	d, err := s.GetByID(draftID)
	if err != nil || d == nil {
		return d, err
	}

	var snapshot models.DraftHistoryModel
	if err := s.db.Where("draft_id = ? AND version = ?", draftID, version).
		First(&snapshot).Error; err != nil {
		return nil, fmt.Errorf("version %d not found", version)
	}

	history := models.DraftHistoryModel{
		DraftID:          d.ID,
		Version:          d.Version,
		Title:            d.Title,
		Text:             d.Text,
		TypeSpecificData: d.TypeSpecificData,
		SavedAt:          time.Now(),
		IsFullSnapshot:   true,
	}
	s.db.Create(&history)

	updates := map[string]interface{}{
		"title":   snapshot.Title,
		"text":    snapshot.Text,
		"version": d.Version + 1,
	}
	if snapshot.TypeSpecificData != nil {
		raw, _ := json.Marshal(snapshot.TypeSpecificData)
		updates["type_specific_data"] = string(raw)
	}
	return d, s.db.Model(d).Updates(updates).Error
}

func (s *Service) GetHistoryVersion(draftID string, version int) (*models.DraftHistoryModel, error) {
	var snapshot models.DraftHistoryModel
	if err := s.db.Where("draft_id = ? AND version = ?", draftID, version).First(&snapshot).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &snapshot, nil
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/drafts", authMW)
	g.GET("", h.list)
	g.POST("", h.create)
	g.GET("/by-ref/:refType/new", h.getNewByRef)
	g.GET("/by-ref/:refType/:refId", h.getByRef)
	g.GET("/:id", h.get)
	g.PUT("/:id", h.update)
	g.PATCH("/:id", h.update) // legacy compatibility
	g.DELETE("/:id", h.delete)
	g.POST("/:id/publish", h.publish)
	g.GET("/:id/history", h.history)
	g.GET("/:id/history/:version", h.historyVersion)
	g.POST("/:id/restore/:version", h.restore)
}

func (h *Handler) list(c *gin.Context) {
	q := pagination.FromContext(c)
	refType := c.Query("ref_type")
	if refType == "" {
		refType = c.Query("refType")
	}
	var rtPtr *string
	if refType != "" {
		rtPtr = &refType
	}
	items, pag, err := h.svc.List(q, rtPtr)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	out := make([]draftResponse, len(items))
	for i, d := range items {
		out[i] = toResponse(&d)
	}
	response.Paged(c, out, pag)
}

func (h *Handler) get(c *gin.Context) {
	d, err := h.svc.GetByID(c.Param("id"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if d == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, toResponse(d))
}

func (h *Handler) getByRef(c *gin.Context) {
	refType := c.Param("refType")
	refID := c.Param("refId")
	d, err := h.svc.GetByRef(refType, refID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.NotFound(c)
			return
		}
		response.InternalError(c, err)
		return
	}
	if d == nil {
		response.OK(c, nil)
		return
	}
	response.OK(c, toResponse(d))
}

func (h *Handler) getNewByRef(c *gin.Context) {
	items, err := h.svc.GetNewByRefType(c.Param("refType"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	out := make([]draftResponse, len(items))
	for i, d := range items {
		out[i] = toResponse(&d)
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) create(c *gin.Context) {
	var dto CreateDraftDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	d, err := h.svc.Create(&dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, toResponse(d))
}

func (h *Handler) update(c *gin.Context) {
	var dto UpdateDraftDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	d, err := h.svc.Update(c.Param("id"), &dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if d == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, toResponse(d))
}

func (h *Handler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Param("id")); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) publish(c *gin.Context) {
	targetID, err := h.svc.Publish(c.Param("id"))
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"target_id": targetID})
}

func (h *Handler) history(c *gin.Context) {
	history, err := h.svc.GetHistory(c.Param("id"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, gin.H{"data": history})
}

func (h *Handler) historyVersion(c *gin.Context) {
	var version int
	fmt.Sscanf(c.Param("version"), "%d", &version)
	if version <= 0 {
		response.BadRequest(c, "version is required")
		return
	}
	snapshot, err := h.svc.GetHistoryVersion(c.Param("id"), version)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if snapshot == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, gin.H{
		"title":              snapshot.Title,
		"text":               snapshot.Text,
		"version":            snapshot.Version,
		"type_specific_data": snapshot.TypeSpecificData,
		"saved_at":           snapshot.SavedAt,
	})
}

func (h *Handler) restore(c *gin.Context) {
	var body struct {
		Version int `json:"version" uri:"version"`
	}
	c.ShouldBindUri(&body)
	if body.Version == 0 {
		fmt.Sscanf(c.Param("version"), "%d", &body.Version)
	}
	if body.Version == 0 {
		response.BadRequest(c, "version is required")
		return
	}
	d, err := h.svc.RestoreVersion(c.Param("id"), body.Version)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, toResponse(d))
}
