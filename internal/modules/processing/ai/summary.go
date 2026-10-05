package ai

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/taskqueue"
	"gorm.io/gorm"
)

const (
	TaskTypeSummary = "ai:summary"
)

var (
	errSummaryArticleNotFound = errors.New("article not found or empty")
	errSummaryDisabled        = errors.New("AI summary is disabled")
	errNoAIProvider           = errors.New("no enabled AI provider")
	errSummaryNotFound        = errors.New("summary not found")
)

// An article has a single summary. Its language comes only from ai.aiSummaryTargetLanguage, never from the
// request: generating per visitor language would let anyone trigger paid model calls for any language.

// summaryKey is the task dedup key for an article's summary.
func summaryKey(refID string) string {
	return refID
}

func summaryHash(refID string) string {
	h := sha256.Sum256([]byte(refID))
	return fmt.Sprintf("%x", h)
}

func normalizeLanguageCode(lang string) string {
	code := strings.TrimSpace(strings.ToLower(lang))
	if idx := strings.Index(code, ","); idx >= 0 {
		code = strings.TrimSpace(code[:idx])
	}
	if idx := strings.Index(code, "-"); idx >= 0 {
		code = strings.TrimSpace(code[:idx])
	}
	return code
}

// summaryTargetLanguage returns the language name for the prompt; "auto" (or empty) keeps the article's language.
func summaryTargetLanguage(cfg *config.FullConfig) string {
	if cfg == nil {
		return ""
	}
	raw := strings.TrimSpace(cfg.AI.AISummaryTargetLanguage)
	code := normalizeLanguageCode(raw)
	if code == "" || code == "auto" {
		return ""
	}
	if name, ok := languageCodeToName[code]; ok {
		return name
	}
	return raw
}

// GetSummary returns the stored summary of an article.
func (s *Service) GetSummary(articleID string) (*models.AISummaryModel, error) {
	var summary models.AISummaryModel
	err := s.db.Where("ref_id = ?", articleID).Order("created_at DESC").First(&summary).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

func (s *Service) summaryProvider() (*config.FullConfig, *config.AIProvider, error) {
	cfg, err := s.cfgSvc.Get()
	if err != nil {
		return nil, nil, err
	}
	if cfg == nil || !cfg.AI.EnableSummary {
		return nil, nil, errSummaryDisabled
	}
	provider := selectAIProvider(cfg.AI, cfg.AI.SummaryModel)
	if provider == nil {
		return nil, nil, errNoAIProvider
	}
	return cfg, provider, nil
}

// saveSummary stores summary as the article's only summary (older ones, e.g. per-language ones, are dropped).
func (s *Service) saveSummary(refID, summary string) (*models.AISummaryModel, error) {
	model := models.AISummaryModel{Hash: summaryHash(refID), Summary: summary, RefID: refID}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("ref_id = ?", refID).Delete(&models.AISummaryModel{}).Error; err != nil {
			return err
		}
		return tx.Create(&model).Error
	})
	if err != nil {
		return nil, err
	}
	return &model, nil
}

// GenerateSummary returns the stored summary, generating it when missing or when force is set.
// Concurrent calls for one article share a single model call.
func (s *Service) GenerateSummary(refID string, force bool) (*models.AISummaryModel, error) {
	if !force {
		if existing, err := s.GetSummary(refID); err != nil || existing != nil {
			return existing, err
		}
	}
	result, err, _ := s.summaryFlight.Do(refID, func() (interface{}, error) {
		if !force {
			if existing, err := s.GetSummary(refID); err != nil || existing != nil {
				return existing, err
			}
		}
		cfg, provider, err := s.summaryProvider()
		if err != nil {
			return nil, err
		}
		_, title, text := s.fetchArticleInfo(refID)
		if text == "" {
			return nil, errSummaryArticleNotFound
		}
		summary, err := callAI(provider, title, text, summaryTargetLanguage(cfg))
		if err != nil {
			return nil, err
		}
		return s.saveSummary(refID, summary)
	})
	if err != nil {
		return nil, err
	}
	summary, _ := result.(*models.AISummaryModel)
	return summary, nil
}

// AutoGenerateSummary runs after a post or note is published when ai.enableAutoGenerateSummary is on.
func (s *Service) AutoGenerateSummary(refID string) {
	cfg, err := s.cfgSvc.Get()
	if err != nil || cfg == nil || !cfg.AI.EnableSummary || !cfg.AI.EnableAutoGenerateSummary {
		return
	}
	_, _ = s.EnqueueSummary(context.Background(), refID, "", "")
}

// GetDeepReading returns the cached deep reading for a given articleID.
func (s *Service) GetDeepReading(articleID string) (*models.AIDeepReadingModel, error) {
	h := sha256.Sum256([]byte(articleID))
	hash := fmt.Sprintf("%x", h)
	var dr models.AIDeepReadingModel
	if err := s.db.Where("hash = ?", hash).First(&dr).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &dr, nil
}

// EnqueueSummary creates an AI summary task (or returns existing dedup task).
func (s *Service) EnqueueSummary(ctx context.Context, refID, refType, title string) (*taskqueue.Task, error) {
	refID = strings.TrimSpace(refID)
	refType = strings.TrimSpace(refType)
	title = strings.TrimSpace(title)
	if refID == "" {
		return nil, errors.New("refId is required")
	}

	// Compatibility: allow callers to provide only refId.
	if refType == "" || title == "" {
		detectedRefType, detectedTitle, text := s.fetchArticleInfo(refID)
		if text == "" {
			return nil, errSummaryArticleNotFound
		}
		if refType == "" {
			refType = detectedRefType
		}
		if title == "" {
			title = detectedTitle
		}
	}

	payload := SummaryPayload{RefID: refID, RefType: refType, Title: title}
	task, err := s.taskSvc.Enqueue(ctx, TaskTypeSummary, payload, summaryKey(refID), refID)
	if err != nil {
		return nil, err
	}

	// Execute immediately in a goroutine (in production use a worker pool)
	if task.Status == taskqueue.TaskPending {
		go s.executeSummary(context.Background(), task.ID, payload)
	}

	return task, nil
}

// GenerateSummaryStream streams the article summary via SSE. A stored summary is sent as is;
// otherwise one is generated only when allowGenerate is set.
func (s *Service) GenerateSummaryStream(c *gin.Context, articleID string, allowGenerate bool) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	sendEvent := func(eventType, data string) {
		fmt.Fprintf(c.Writer, "data: %s\n\n", fmt.Sprintf(`{"type":%q,"data":%s}`, eventType, data))
		c.Writer.Flush()
	}
	sendError := func(err error) {
		errJSON, _ := jsonMarshal(err.Error())
		sendEvent("error", string(errJSON))
	}
	sendSummary := func(summary string) {
		tokenJSON, _ := jsonMarshal(summary)
		sendEvent("token", string(tokenJSON))
		sendEvent("done", "null")
	}

	if existing, err := s.GetSummary(articleID); err != nil {
		sendError(err)
		return
	} else if existing != nil {
		sendSummary(existing.Summary)
		return
	}
	if !allowGenerate {
		sendError(errSummaryNotFound)
		return
	}

	streamed := false
	result, err, _ := s.summaryFlight.Do(articleID, func() (interface{}, error) {
		streamed = true
		cfg, provider, err := s.summaryProvider()
		if err != nil {
			return nil, err
		}
		_, title, text := s.fetchArticleInfo(articleID)
		if text == "" {
			return nil, errSummaryArticleNotFound
		}
		rawSummary, err := callAIStream(provider, title, text, summaryTargetLanguage(cfg), func(token string) {
			tokenJSON, _ := jsonMarshal(token)
			sendEvent("token", string(tokenJSON))
		})
		if err != nil {
			return nil, err
		}
		summary, err := extractSummaryFromAIResponse(rawSummary)
		if err != nil {
			return nil, err
		}
		return s.saveSummary(articleID, summary)
	})
	if err != nil {
		sendError(err)
		return
	}
	if streamed {
		sendEvent("done", "null")
		return
	}
	// Another request generated it meanwhile.
	if summary, ok := result.(*models.AISummaryModel); ok && summary != nil {
		sendSummary(summary.Summary)
		return
	}
	sendError(errSummaryNotFound)
}

func (s *Service) executeSummary(ctx context.Context, taskID string, payload SummaryPayload) {
	_ = s.taskSvc.UpdateStatus(ctx, taskID, taskqueue.TaskRunning, nil, "")

	cfg, provider, err := s.summaryProvider()
	if err != nil {
		_ = s.taskSvc.UpdateStatus(ctx, taskID, taskqueue.TaskFailed, nil, err.Error())
		return
	}

	text, err := s.fetchArticleText(payload.RefID, payload.RefType)
	if err != nil || text == "" {
		_ = s.taskSvc.UpdateStatus(ctx, taskID, taskqueue.TaskFailed, nil, "article not found or empty")
		return
	}

	summary, err := callAI(provider, payload.Title, text, summaryTargetLanguage(cfg))
	if err != nil {
		_ = s.taskSvc.UpdateStatus(ctx, taskID, taskqueue.TaskFailed, nil, err.Error())
		return
	}

	// Cancelled while the model was running: drop the result.
	if task, err := s.taskSvc.GetByID(ctx, taskID); err == nil && task != nil && task.Status == taskqueue.TaskCancelled {
		return
	}
	if _, err := s.saveSummary(payload.RefID, summary); err != nil {
		_ = s.taskSvc.UpdateStatus(ctx, taskID, taskqueue.TaskFailed, nil, err.Error())
		return
	}

	_ = s.taskSvc.UpdateStatus(ctx, taskID, taskqueue.TaskCompleted, gin.H{"summary": summary}, "")
}

// fetchArticleInfo returns (refType, title, text) for an article by ID.
func (s *Service) fetchArticleInfo(id string) (refType, title, text string) {
	var p models.PostModel
	if s.db.Select("title, text").First(&p, "id = ?", id).Error == nil {
		return "post", p.Title, p.Text
	}
	var n models.NoteModel
	if s.db.Select("title, text").First(&n, "id = ?", id).Error == nil {
		return "note", n.Title, n.Text
	}
	var pg models.PageModel
	if s.db.Select("title, text").First(&pg, "id = ?", id).Error == nil {
		return "page", pg.Title, pg.Text
	}
	return "", "", ""
}

func (s *Service) fetchArticleText(refID, refType string) (string, error) {
	switch refType {
	case "post":
		var p models.PostModel
		if err := s.db.Select("text").First(&p, "id = ?", refID).Error; err != nil {
			return "", err
		}
		return p.Text, nil
	case "note":
		var n models.NoteModel
		if err := s.db.Select("text").First(&n, "id = ?", refID).Error; err != nil {
			return "", err
		}
		return n.Text, nil
	case "page":
		var pg models.PageModel
		if err := s.db.Select("text").First(&pg, "id = ?", refID).Error; err != nil {
			return "", err
		}
		return pg.Text, nil
	}
	return "", fmt.Errorf("unsupported ref type: %s", refType)
}

// ArticleIsPublic reports whether visitors may see an article, so its summary may be read or generated:
// unpublished, password-protected or not-yet-public content must not leak through its summary.
func (s *Service) ArticleIsPublic(id string) bool {
	var p models.PostModel
	if s.db.Select("id, is_published").First(&p, "id = ?", id).Error == nil {
		return p.IsPublished
	}
	var n models.NoteModel
	if s.db.Select("id, is_published, password_hash, public_at").First(&n, "id = ?", id).Error == nil {
		if !n.IsPublished || n.Password != "" {
			return false
		}
		return n.PublicAt == nil || !n.PublicAt.After(time.Now())
	}
	var pg models.PageModel
	return s.db.Select("id").First(&pg, "id = ?", id).Error == nil
}
