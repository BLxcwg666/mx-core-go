package ai

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/taskqueue"
	"gorm.io/gorm"
)

const (
	TaskTypeSummary = "ai:summary"
)

var errSummaryArticleNotFound = errors.New("article not found or empty")

// summaryKey generates the dedup key for a summary task.
func summaryKey(refID, lang string) string {
	if lang == "" {
		lang = "default"
	}
	return fmt.Sprintf("%s:%s", refID, lang)
}

// hashKey generates the cache hash for a summary.
func hashKey(refID, lang string) string {
	h := sha256.Sum256([]byte(refID + ":" + lang))
	return fmt.Sprintf("%x", h)
}

func normalizeLanguageCode(lang string) string {
	code := strings.TrimSpace(strings.ToLower(lang))
	if code == "" {
		return defaultSummaryLangCode
	}
	if idx := strings.Index(code, ","); idx >= 0 {
		code = strings.TrimSpace(code[:idx])
	}
	if idx := strings.Index(code, "-"); idx >= 0 {
		code = strings.TrimSpace(code[:idx])
	}
	if code == "" {
		return defaultSummaryLangCode
	}
	return code
}

func resolveSummaryTargetLanguageName(lang string) string {
	code := normalizeLanguageCode(lang)
	if code == "auto" {
		code = defaultSummaryLangCode
	}
	if name, ok := languageCodeToName[code]; ok {
		return name
	}
	return languageCodeToName[defaultSummaryLangCode]
}

// GetSummary returns a cached summary for a given articleID and lang.
func (s *Service) GetSummary(articleID, lang string) (*models.AISummaryModel, error) {
	hash := hashKey(articleID, lang)
	var summary models.AISummaryModel
	if err := s.db.Where("hash = ?", hash).First(&summary).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &summary, nil
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
func (s *Service) EnqueueSummary(ctx context.Context, refID, refType, title, lang string) (*taskqueue.Task, error) {
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

	if lang == "" {
		cfg, _ := s.cfgSvc.Get()
		if cfg != nil {
			lang = cfg.AI.AISummaryTargetLanguage
		}
	}
	if lang == "" {
		lang = "zh-CN"
	}

	payload := SummaryPayload{RefID: refID, RefType: refType, Title: title, Lang: lang}
	task, err := s.taskSvc.Enqueue(ctx, TaskTypeSummary, payload, summaryKey(refID, lang), refID)
	if err != nil {
		return nil, err
	}

	// Execute immediately in a goroutine (in production use a worker pool)
	if task.Status == taskqueue.TaskPending {
		go s.executeSummary(context.Background(), task.ID, payload)
	}

	return task, nil
}

// GenerateSummaryStream generates a summary via SSE streaming.
// Writes SSE events to the gin.Context directly.
func (s *Service) GenerateSummaryStream(c *gin.Context, articleID, lang string) {
	if lang == "" {
		cfg, _ := s.cfgSvc.Get()
		if cfg != nil {
			lang = cfg.AI.AISummaryTargetLanguage
		}
	}
	if lang == "" {
		lang = "zh-CN"
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	sendEvent := func(eventType, data string) {
		fmt.Fprintf(c.Writer, "data: %s\n\n", fmt.Sprintf(`{"type":%q,"data":%s}`, eventType, data))
		c.Writer.Flush()
	}

	cfg, err := s.cfgSvc.Get()
	if err != nil || !cfg.AI.EnableSummary {
		sendEvent("error", `"AI summary is disabled"`)
		return
	}

	provider := selectAIProvider(cfg.AI, cfg.AI.SummaryModel)
	if provider == nil {
		sendEvent("error", `"no enabled AI provider"`)
		return
	}

	_, title, text := s.fetchArticleInfo(articleID)
	if text == "" {
		sendEvent("error", `"article not found or empty"`)
		return
	}

	rawSummary, err := callAIStream(provider, title, text, lang, func(token string) {
		tokenJSON, _ := jsonMarshal(token)
		sendEvent("token", string(tokenJSON))
	})
	if err != nil {
		errJSON, _ := jsonMarshal(err.Error())
		sendEvent("error", string(errJSON))
		return
	}
	summary, err := extractSummaryFromAIResponse(rawSummary)
	if err != nil {
		errJSON, _ := jsonMarshal(err.Error())
		sendEvent("error", string(errJSON))
		return
	}

	hash := hashKey(articleID, lang)
	summaryModel := models.AISummaryModel{
		Hash:    hash,
		Summary: summary,
		RefID:   articleID,
		Lang:    lang,
	}
	s.db.Where("hash = ?", hash).Assign(summaryModel).FirstOrCreate(&summaryModel)

	sendEvent("done", "null")
}

func (s *Service) executeSummary(ctx context.Context, taskID string, payload SummaryPayload) {
	s.taskSvc.UpdateStatus(ctx, taskID, taskqueue.TaskRunning, nil, "")

	cfg, err := s.cfgSvc.Get()
	if err != nil || !cfg.AI.EnableSummary {
		s.taskSvc.UpdateStatus(ctx, taskID, taskqueue.TaskFailed, nil, "AI summary is disabled")
		return
	}

	provider := selectAIProvider(cfg.AI, cfg.AI.SummaryModel)
	if provider == nil {
		s.taskSvc.UpdateStatus(ctx, taskID, taskqueue.TaskFailed, nil, "no enabled AI provider")
		return
	}

	text, err := s.fetchArticleText(payload.RefID, payload.RefType)
	if err != nil || text == "" {
		s.taskSvc.UpdateStatus(ctx, taskID, taskqueue.TaskFailed, nil, "article not found or empty")
		return
	}

	summary, err := callAI(provider, payload.Title, text, payload.Lang)
	if err != nil {
		s.taskSvc.UpdateStatus(ctx, taskID, taskqueue.TaskFailed, nil, err.Error())
		return
	}

	hash := hashKey(payload.RefID, payload.Lang)
	summaryModel := models.AISummaryModel{
		Hash:    hash,
		Summary: summary,
		RefID:   payload.RefID,
		Lang:    payload.Lang,
	}
	s.db.Where("hash = ?", hash).Assign(summaryModel).FirstOrCreate(&summaryModel)

	s.taskSvc.UpdateStatus(ctx, taskID, taskqueue.TaskCompleted, gin.H{"summary": summary}, "")
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
