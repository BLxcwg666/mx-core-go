package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type CreateWebhookDTO struct {
	PayloadURL string   `json:"payloadUrl" binding:"required,url"`
	Events     []string `json:"events"      binding:"required,min=1"`
	Enabled    *bool    `json:"enabled"`
	Secret     string   `json:"secret"`
	Scope      *int     `json:"scope"`
}

type UpdateWebhookDTO struct {
	PayloadURL *string  `json:"payloadUrl"`
	Events     []string `json:"events"`
	Enabled    *bool    `json:"enabled"`
	Secret     *string  `json:"secret"`
	Scope      *int     `json:"scope"`
}

var webhookEventEnum = []string{
	"GATEWAY_CONNECT",
	"GATEWAY_DISCONNECT",
	"VISITOR_ONLINE",
	"VISITOR_OFFLINE",
	"AUTH_FAILED",
	"COMMENT_CREATE",
	"COMMENT_DELETE",
	"COMMENT_UPDATE",
	"POST_CREATE",
	"POST_UPDATE",
	"POST_DELETE",
	"NOTE_CREATE",
	"NOTE_UPDATE",
	"NOTE_DELETE",
	"PAGE_CREATE",
	"PAGE_UPDATE",
	"PAGE_DELETE",
	"TOPIC_CREATE",
	"TOPIC_UPDATE",
	"TOPIC_DELETE",
	"CATEGORY_CREATE",
	"CATEGORY_UPDATE",
	"CATEGORY_DELETE",
	"SAY_CREATE",
	"SAY_DELETE",
	"SAY_UPDATE",
	"LINK_APPLY",
	"RECENTLY_CREATE",
	"RECENTLY_UPDATE",
	"RECENTLY_DELETE",
	"TRANSLATION_CREATE",
	"TRANSLATION_UPDATE",
	"CONTENT_REFRESH",
	"IMAGE_REFRESH",
	"IMAGE_FETCH",
	"ADMIN_NOTIFICATION",
	"STDOUT",
	"ACTIVITY_LIKE",
	"ACTIVITY_UPDATE_PRESENCE",
	"ACTIVITY_LEAVE_PRESENCE",
	"ARTICLE_READ_COUNT_UPDATE",
}

var acceptedWebhookEvents = func() map[string]struct{} {
	out := make(map[string]struct{}, len(webhookEventEnum))
	for _, event := range webhookEventEnum {
		out[event] = struct{}{}
	}
	return out
}()

func normalizeWebhookEvents(events []string) []string {
	if len(events) == 0 {
		return []string{}
	}

	seen := map[string]struct{}{}
	out := make([]string, 0, len(events))
	for _, event := range events {
		next := strings.TrimSpace(event)
		if next == "" {
			continue
		}
		if strings.EqualFold(next, "all") {
			return []string{"all"}
		}
		next = strings.ToUpper(next)
		if _, ok := acceptedWebhookEvents[next]; !ok {
			continue
		}
		if _, ok := seen[next]; ok {
			continue
		}
		seen[next] = struct{}{}
		out = append(out, next)
	}
	return out
}

type webhookResponse struct {
	ID         string    `json:"id"`
	PayloadURL string    `json:"payloadUrl"`
	Events     []string  `json:"events"`
	Enabled    bool      `json:"enabled"`
	Scope      int       `json:"scope"`
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

func (s *Service) List() ([]models.WebhookModel, error) {
	var items []models.WebhookModel
	return items, s.db.Order("created_at DESC").Find(&items).Error
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
	events := normalizeWebhookEvents(dto.Events)
	if len(events) == 0 {
		return nil, fmt.Errorf("events is empty")
	}

	secretBytes := make([]byte, 20)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, err
	}
	secret := strings.TrimSpace(dto.Secret)
	if secret == "" {
		secret = hex.EncodeToString(secretBytes)
	}
	scope := 4
	if dto.Scope != nil {
		scope = *dto.Scope
	}

	w := models.WebhookModel{
		PayloadURL: dto.PayloadURL,
		Events:     events,
		Secret:     secret,
		Scope:      scope,
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
		events := normalizeWebhookEvents(dto.Events)
		if len(events) == 0 {
			return nil, fmt.Errorf("events is empty")
		}
		updates["events"] = events
	}
	if dto.Enabled != nil {
		updates["enabled"] = *dto.Enabled
	}
	if dto.Scope != nil {
		updates["scope"] = *dto.Scope
	}
	if dto.Secret != nil {
		updates["secret"] = strings.TrimSpace(*dto.Secret)
	}
	return w, s.db.Model(w).Updates(updates).Error
}

func (s *Service) Delete(id string) error {
	return s.db.Delete(&models.WebhookModel{}, "id = ?", id).Error
}

// Dispatch sends an event payload to all matching webhooks.
func (s *Service) Dispatch(event string, payload interface{}) {
	var hooks []models.WebhookModel
	s.db.Where("enabled = ?", true).Find(&hooks)

	for _, hook := range hooks {
		if !webhookContainsEvent(hook.Events, event) {
			continue
		}
		go s.deliver(hook, event, payload)
	}
}

func (s *Service) deliver(hook models.WebhookModel, event string, payload interface{}) {
	body, _ := json.Marshal(payload)
	payloadString := string(body)

	signature := signWithHash(sha1.New, hook.Secret, payloadString)
	signature256 := signWithHash(sha256.New, hook.Secret, payloadString)
	timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
	headers := map[string]string{
		"X-Webhook-Signature":    signature,
		"X-Webhook-Event":        event,
		"X-Webhook-Id":           hook.ID,
		"X-Webhook-Timestamp":    timestamp,
		"X-Webhook-Signature256": signature256,
	}

	req, err := http.NewRequest("POST", hook.PayloadURL, bytes.NewReader(body))
	if err != nil {
		s.logEvent(hook.ID, event, headers, payloadString, nil, false, 0, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.logEvent(hook.ID, event, headers, payloadString, nil, false, 0, err.Error())
		return
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	s.logEvent(hook.ID, event, headers, payloadString, map[string]interface{}{
		"headers":   resp.Header,
		"data":      parseJSONOrString(bodyBytes),
		"timestamp": time.Now().UnixMilli(),
		"status":    resp.Status,
	}, resp.StatusCode >= 200 && resp.StatusCode < 300, resp.StatusCode, "")
}

func (s *Service) logEvent(hookID, event string, headers map[string]string, payload string, respData interface{}, success bool, status int, errMsg string) {
	log := models.WebhookEventModel{
		HookID:    hookID,
		Event:     event,
		Headers:   toJSONString(headers),
		Payload:   payload,
		Response:  toJSONString(respData),
		Success:   success,
		Status:    status,
		Timestamp: time.Now(),
	}
	if errMsg != "" {
		log.Response = toJSONString(map[string]interface{}{"error": errMsg})
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
	var payload interface{}
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		payload = event.Payload
	}
	go s.deliver(*hook, event.Event, payload)
	return nil
}

func (s *Service) ClearEventsByHookID(hookID string) error {
	return s.db.Where("hook_id = ?", hookID).Delete(&models.WebhookEventModel{}).Error
}

func webhookContainsEvent(events []string, event string) bool {
	event = strings.ToUpper(strings.TrimSpace(event))
	for _, item := range events {
		next := strings.ToUpper(strings.TrimSpace(item))
		if next == "ALL" || next == event {
			return true
		}
	}
	return false
}

func parseJSONOrString(data []byte) interface{} {
	if len(data) == 0 {
		return ""
	}
	var out interface{}
	if err := json.Unmarshal(data, &out); err == nil {
		return out
	}
	return string(data)
}

func toJSONString(v interface{}) string {
	if v == nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func signWithHash(newHash func() hash.Hash, secret, payload string) string {
	mac := hmac.New(newHash, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
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

	g.GET("/events", h.listEventsEnum)
	g.GET("/dispatches", h.listEvents)
	g.POST("/redispatch/:id", h.redispatch)
	g.DELETE("/clear/:id", h.clearEvents)
	g.GET("/:id", h.listEventsByHook)
}

func (h *Handler) list(c *gin.Context) {
	items, err := h.svc.List()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	out := make([]webhookResponse, len(items))
	for i, w := range items {
		out[i] = toResponse(&w)
	}
	response.OK(c, out)
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

func (h *Handler) listEventsEnum(c *gin.Context) {
	response.OK(c, webhookEventEnum)
}

func (h *Handler) listEvents(c *gin.Context) {
	q := pagination.FromContext(c)
	hookID := c.Query("hookId")
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
