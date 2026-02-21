package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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

type CreateWebhookDTO struct {
	PayloadURL string   `json:"payload_url" binding:"required,url"`
	Events     []string `json:"events"      binding:"required,min=1"`
	Enabled    *bool    `json:"enabled"`
	Scope      string   `json:"scope"`
}

type UpdateWebhookDTO struct {
	PayloadURL *string  `json:"payload_url"`
	Events     []string `json:"events"`
	Enabled    *bool    `json:"enabled"`
	Scope      *string  `json:"scope"`
}

type webhookResponse struct {
	ID         string    `json:"id"`
	PayloadURL string    `json:"payload_url"`
	Events     []string  `json:"events"`
	Enabled    bool      `json:"enabled"`
	Scope      string    `json:"scope"`
	Created    time.Time `json:"created"`
	Modified   time.Time `json:"modified"`
}

func toResponse(w *models.WebhookModel) webhookResponse {
	events := w.Events
	if events == nil {
		events = []string{}
	}
	return webhookResponse{
		ID: w.ID, PayloadURL: w.PayloadURL, Events: events,
		Enabled: w.Enabled, Scope: w.Scope,
		Created: w.CreatedAt, Modified: w.UpdatedAt,
	}
}

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) List(q pagination.Query) ([]models.WebhookModel, response.Pagination, error) {
	tx := s.db.Model(&models.WebhookModel{}).Order("created_at DESC")
	var items []models.WebhookModel
	pag, err := pagination.Paginate(tx, q, &items)
	return items, pag, err
}

func (s *Service) GetByID(id string) (*models.WebhookModel, error) {
	var w models.WebhookModel
	if err := s.db.First(&w, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &w, nil
}

func (s *Service) Create(dto *CreateWebhookDTO) (*models.WebhookModel, error) {
	secretBytes := make([]byte, 20)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, err
	}
	w := models.WebhookModel{
		PayloadURL: dto.PayloadURL,
		Events:     dto.Events,
		Secret:     hex.EncodeToString(secretBytes),
		Scope:      dto.Scope,
		Enabled:    true,
	}
	if dto.Enabled != nil {
		w.Enabled = *dto.Enabled
	}
	return &w, s.db.Create(&w).Error
}

func (s *Service) Update(id string, dto *UpdateWebhookDTO) (*models.WebhookModel, error) {
	w, err := s.GetByID(id)
	if err != nil || w == nil {
		return w, err
	}
	updates := map[string]interface{}{}
	if dto.PayloadURL != nil {
		updates["payload_url"] = *dto.PayloadURL
	}
	if dto.Events != nil {
		updates["events"] = dto.Events
	}
	if dto.Enabled != nil {
		updates["enabled"] = *dto.Enabled
	}
	if dto.Scope != nil {
		updates["scope"] = *dto.Scope
	}
	return w, s.db.Model(w).Updates(updates).Error
}

func (s *Service) Delete(id string) error {
	return s.db.Delete(&models.WebhookModel{}, "id = ?", id).Error
}

// Dispatch sends an event payload to all matching webhooks.
func (s *Service) Dispatch(event string, payload interface{}) {
	var hooks []models.WebhookModel
	s.db.Where("enabled = ? AND JSON_CONTAINS(events, ?)", true, fmt.Sprintf("%q", event)).
		Find(&hooks)

	for _, hook := range hooks {
		go s.deliver(hook, event, payload)
	}
}

func (s *Service) deliver(hook models.WebhookModel, event string, payload interface{}) {
	body, _ := json.Marshal(payload)
	mac := hmac.New(sha256.New, []byte(hook.Secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest("POST", hook.PayloadURL, bytes.NewReader(body))
	if err != nil {
		s.logEvent(hook.ID, event, payload, nil, false, 0, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Mx-Event", event)
	req.Header.Set("X-Mx-Signature-256", sig)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.logEvent(hook.ID, event, payload, nil, false, 0, err.Error())
		return
	}
	defer resp.Body.Close()

	s.logEvent(hook.ID, event, payload, map[string]interface{}{
		"status": resp.Status,
	}, resp.StatusCode >= 200 && resp.StatusCode < 300, resp.StatusCode, "")
}

func (s *Service) logEvent(hookID, event string, payload, respData interface{}, success bool, status int, errMsg string) {
	log := models.WebhookEventModel{
		HookID:    hookID,
		Event:     event,
		Payload:   toMap(payload),
		Response:  toMap(respData),
		Success:   success,
		Status:    status,
		Timestamp: time.Now(),
	}
	if errMsg != "" {
		log.Response = map[string]interface{}{"error": errMsg}
	}
	s.db.Create(&log)
}

func (s *Service) ListEvents(q pagination.Query, hookID *string) ([]models.WebhookEventModel, response.Pagination, error) {
	tx := s.db.Model(&models.WebhookEventModel{}).Order("timestamp DESC")
	if hookID != nil {
		tx = tx.Where("hook_id = ?", *hookID)
	}
	var items []models.WebhookEventModel
	pag, err := pagination.Paginate(tx, q, &items)
	return items, pag, err
}

func (s *Service) GetEventByID(id string) (*models.WebhookEventModel, error) {
	var item models.WebhookEventModel
	if err := s.db.First(&item, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

func (s *Service) Redispatch(eventID string) error {
	event, err := s.GetEventByID(eventID)
	if err != nil {
		return err
	}
	if event == nil {
		return fmt.Errorf("event not found")
	}
	hook, err := s.GetByID(event.HookID)
	if err != nil {
		return err
	}
	if hook == nil {
		return fmt.Errorf("hook not found")
	}
	if !hook.Enabled {
		return fmt.Errorf("hook is disabled")
	}
	go s.deliver(*hook, event.Event, event.Payload)
	return nil
}

func (s *Service) ClearEventsByHookID(hookID string) error {
	return s.db.Where("hook_id = ?", hookID).Delete(&models.WebhookEventModel{}).Error
}

func toMap(v interface{}) map[string]interface{} {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	return m
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/webhooks", authMW)
	g.GET("", h.list)
	g.POST("", h.create)
	g.PUT("/:id", h.update)
	g.PATCH("/:id", h.update)
	g.DELETE("/:id", h.delete)

	g.GET("/events", h.listEvents)
	g.POST("/redispatch/:id", h.redispatch)
	g.DELETE("/clear/:id", h.clearEvents)
	g.GET("/:id", h.listEventsByHook)
}

func (h *Handler) list(c *gin.Context) {
	q := pagination.FromContext(c)
	items, pag, err := h.svc.List(q)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	out := make([]webhookResponse, len(items))
	for i, w := range items {
		out[i] = toResponse(&w)
	}
	response.Paged(c, out, pag)
}

func (h *Handler) create(c *gin.Context) {
	var dto CreateWebhookDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	w, err := h.svc.Create(&dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, toResponse(w))
}

func (h *Handler) update(c *gin.Context) {
	var dto UpdateWebhookDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	w, err := h.svc.Update(c.Param("id"), &dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if w == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, toResponse(w))
}

func (h *Handler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Param("id")); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) listEvents(c *gin.Context) {
	q := pagination.FromContext(c)
	hookID := c.Query("hook_id")
	var hookIDPtr *string
	if hookID != "" {
		hookIDPtr = &hookID
	}
	items, pag, err := h.svc.ListEvents(q, hookIDPtr)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Paged(c, items, pag)
}

func (h *Handler) listEventsByHook(c *gin.Context) {
	q := pagination.FromContext(c)
	hookID := c.Param("id")
	items, pag, err := h.svc.ListEvents(q, &hookID)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Paged(c, items, pag)
}

func (h *Handler) redispatch(c *gin.Context) {
	if err := h.svc.Redispatch(c.Param("id")); err != nil {
		if err.Error() == "event not found" || err.Error() == "hook not found" {
			response.NotFoundMsg(c, err.Error())
			return
		}
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) clearEvents(c *gin.Context) {
	if err := h.svc.ClearEventsByHookID(c.Param("id")); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}
