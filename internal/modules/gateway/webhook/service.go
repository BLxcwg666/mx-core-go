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

	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

// Service handles webhook CRUD and delivery.
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

// Dispatch sends an event payload to all matching, enabled webhooks.
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

// normalizeWebhookEvents deduplicates events, uppercases them, and validates
// each against the accepted set. The special value "all" short-circuits.
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
