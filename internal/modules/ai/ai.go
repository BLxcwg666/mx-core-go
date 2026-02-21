package ai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	appcfg "github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/configs"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"github.com/mx-space/core/internal/pkg/taskqueue"
	"gorm.io/gorm"
)

const (
	TaskTypeSummary = "ai:summary"
)

var errSummaryArticleNotFound = errors.New("article not found or empty")

// SummaryPayload is the task payload for summary generation.
type SummaryPayload struct {
	RefID   string `json:"ref_id"`
	RefType string `json:"ref_type"` // post | note | page
	Title   string `json:"title"`
	Lang    string `json:"lang"`
}

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

// Service handles AI operations.
type Service struct {
	db      *gorm.DB
	cfgSvc  *configs.Service
	taskSvc *taskqueue.Service
}

func NewService(db *gorm.DB, cfgSvc *configs.Service, taskSvc *taskqueue.Service) *Service {
	return &Service{db: db, cfgSvc: cfgSvc, taskSvc: taskSvc}
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

	refType, title, text := s.fetchArticleInfo(articleID)
	if text == "" {
		sendEvent("error", `"article not found or empty"`)
		return
	}

	if lang == "auto" {
		lang = "Chinese"
	}

	err = callAIStream(provider, title, text, lang, func(token string) {
		tokenJSON, _ := json.Marshal(token)
		sendEvent("token", string(tokenJSON))
	})
	if err != nil {
		errJSON, _ := json.Marshal(err.Error())
		sendEvent("error", string(errJSON))
		return
	}

	hash := hashKey(articleID, lang)
	summaryModel := models.AISummaryModel{
		Hash:  hash,
		RefID: articleID,
		Lang:  lang,
	}
	summary, _ := callAI(provider, title, text, lang)
	summaryModel.Summary = summary
	s.db.Where("hash = ?", hash).Assign(summaryModel).FirstOrCreate(&summaryModel)

	_ = refType

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

	lang := payload.Lang
	if lang == "auto" || lang == "" {
		lang = "Chinese"
	}
	summary, err := callAI(provider, payload.Title, text, lang)
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

// callAI calls the AI provider to generate a summary.
func callAI(provider *appcfg.AIProvider, title, text, lang string) (string, error) {
	prompt := fmt.Sprintf(
		"Please summarize the following article titled \"%s\" in %s, in 2-4 sentences:\n\n%s",
		title, lang, truncateText(text, 3000),
	)
	return callAIWithPrompt(provider, prompt)
}

func callAIWithPrompt(provider *appcfg.AIProvider, prompt string) (string, error) {
	switch provider.Type {
	case "Anthropic":
		return callAnthropic(provider, prompt)
	default:
		return callOpenAI(provider, prompt)
	}
}

// callAIStream calls AI with streaming and invokes onToken for each chunk.
func callAIStream(provider *appcfg.AIProvider, title, text, lang string, onToken func(string)) error {
	prompt := fmt.Sprintf(
		"Please summarize the following article titled \"%s\" in %s, in 2-4 sentences:\n\n%s",
		title, lang, truncateText(text, 3000),
	)

	var result string
	var err error
	switch provider.Type {
	case "Anthropic":
		result, err = callAnthropic(provider, prompt)
	default:
		result, err = callOpenAIStream(provider, prompt, onToken)
		if err == nil {
			return nil
		}
	}
	if err != nil {
		return err
	}
	onToken(result)
	return nil
}

func callOpenAI(provider *appcfg.AIProvider, prompt string) (string, error) {
	endpoint := provider.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com"
	}
	model := provider.DefaultModel
	if model == "" {
		model = "gpt-4o-mini"
	}

	body, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 300,
	})

	req, err := http.NewRequest("POST", endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Error != nil {
		return "", fmt.Errorf("openai error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty response from AI")
	}
	return result.Choices[0].Message.Content, nil
}

// callOpenAIStream calls OpenAI with stream=true and forwards tokens via onToken.
func callOpenAIStream(provider *appcfg.AIProvider, prompt string, onToken func(string)) (string, error) {
	endpoint := provider.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com"
	}
	model := provider.DefaultModel
	if model == "" {
		model = "gpt-4o-mini"
	}

	body, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 300,
		"stream":     true,
	})

	req, err := http.NewRequest("POST", endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var full string
	buf := make([]byte, 4096)
	remainder := ""
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := remainder + string(buf[:n])
			remainder = ""
			lines := splitLines(chunk)
			for i, line := range lines {
				if i == len(lines)-1 && readErr == nil {
					remainder = line
					continue
				}
				if len(line) < 6 || line[:6] != "data: " {
					continue
				}
				data := line[6:]
				if data == "[DONE]" {
					break
				}
				var event struct {
					Choices []struct {
						Delta struct {
							Content string `json:"content"`
						} `json:"delta"`
					} `json:"choices"`
				}
				if err2 := json.Unmarshal([]byte(data), &event); err2 == nil {
					if len(event.Choices) > 0 && event.Choices[0].Delta.Content != "" {
						token := event.Choices[0].Delta.Content
						onToken(token)
						full += token
					}
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	return full, nil
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}

func unmarshalAIJSON(raw string, out interface{}) error {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```JSON")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	if err := json.Unmarshal([]byte(cleaned), out); err == nil {
		return nil
	}

	start := strings.Index(cleaned, "{")
	end := strings.LastIndex(cleaned, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(cleaned[start:end+1]), out); err == nil {
			return nil
		}
	}

	return fmt.Errorf("invalid JSON response from AI")
}

func callAnthropic(provider *appcfg.AIProvider, prompt string) (string, error) {
	endpoint := provider.Endpoint
	if endpoint == "" {
		endpoint = "https://api.anthropic.com"
	}
	model := provider.DefaultModel
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}

	body, _ := json.Marshal(map[string]interface{}{
		"model":      model,
		"max_tokens": 300,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})

	req, err := http.NewRequest("POST", endpoint+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", provider.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}
	if result.Error != nil {
		return "", fmt.Errorf("anthropic error: %s", result.Error.Message)
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty response from AI")
	}
	return result.Content[0].Text, nil
}

func truncateText(text string, maxLen int) string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	return string(runes[:maxLen]) + "..."
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/ai")

	modelsRoute := g.Group("/models", authMW)
	modelsRoute.GET("", h.getAvailableModels)
	modelsRoute.GET("/:providerId", h.getModelsForProvider)
	modelsRoute.POST("/list", h.fetchModelsList)
	g.POST("/test", authMW, h.testProviderConnection)

	summaries := g.Group("/summaries")
	summaries.GET("/article/:id", h.getSummary)
	summaries.GET("/article/:id/generate", h.streamSummaryGenerate)
	summaries.POST("/generate", h.generateSummary)

	summariesAdmin := g.Group("/summaries", authMW)
	summariesAdmin.GET("", h.listSummaries)
	summariesAdmin.GET("/ref/:id", h.getSummariesByRefID)
	summariesAdmin.POST("/task", h.createSummaryTask)
	summariesAdmin.GET("/task", h.getSummaryTask)
	summariesAdmin.GET("/grouped", h.getGroupedSummaries)
	summariesAdmin.PATCH("/:id", h.updateSummary)
	summariesAdmin.DELETE("/:id", h.deleteSummary)

	g.GET("/deep-readings/article/:id", h.getDeepReading)

	tasks := g.Group("/tasks", authMW)
	tasks.GET("", h.listTasks)
	tasks.GET("/group/:groupKey", h.getTasksByGroup)
	tasks.GET("/:id", h.getTask)
	tasks.DELETE("/group/:groupKey", h.cancelTasksByGroup)
	tasks.DELETE("/:id", h.deleteTask)
	tasks.DELETE("", h.batchDeleteTasks)
	tasks.POST("/:id/cancel", h.cancelTask)
	tasks.POST("/:id/retry", h.retryTask)

	g.POST("/comment-review/test", authMW, h.testCommentReview)
}

// GET /ai/summaries/article/:id?lang=...&onlyDb=...
func (h *Handler) getSummary(c *gin.Context) {
	articleID := c.Param("id")
	lang := c.DefaultQuery("lang", "zh-CN")
	onlyDb := c.Query("onlyDb") == "true" || c.Query("only_db") == "true"

	summary, err := h.svc.GetSummary(articleID, lang)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if summary != nil {
		response.OK(c, summary)
		return
	}
	if onlyDb {
		response.NotFound(c)
		return
	}

	task, err := h.svc.EnqueueSummary(c.Request.Context(), articleID, "", "", lang)
	if err != nil {
		if errors.Is(err, errSummaryArticleNotFound) {
			response.NotFound(c)
			return
		}
		response.InternalError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"message": "summary generation queued",
		"task_id": task.ID,
	})
}

// GET /ai/summaries/article/:id/generate  鈥?SSE streaming
func (h *Handler) streamSummaryGenerate(c *gin.Context) {
	articleID := c.Param("id")
	lang := c.DefaultQuery("lang", "zh-CN")
	h.svc.GenerateSummaryStream(c, articleID, lang)
}

type generateSummaryDTO struct {
	RefID string `json:"refId"    binding:"required"`
	Lang  string `json:"lang"`
}

type createSummaryTaskDTO struct {
	RefID       string `json:"refId"`
	RefIDLegacy string `json:"ref_id"`
	Lang        string `json:"lang"`
}

type updateSummaryDTO struct {
	Summary string `json:"summary" binding:"required"`
}

type modelInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Created int64  `json:"created,omitempty"`
}

type providerModelsResponse struct {
	ProviderID   string      `json:"providerId"`
	ProviderName string      `json:"providerName"`
	ProviderType string      `json:"providerType"`
	Models       []modelInfo `json:"models"`
	Error        string      `json:"error,omitempty"`
}

type fetchModelsDTO struct {
	ProviderID string `json:"providerId"`
	Type       string `json:"type"`
	APIKey     string `json:"apiKey"`
	Endpoint   string `json:"endpoint"`
}

type testConnectionDTO struct {
	ProviderID string `json:"providerId"`
	Type       string `json:"type"`
	APIKey     string `json:"apiKey"`
	Endpoint   string `json:"endpoint"`
	Model      string `json:"model"`
}

// POST /ai/summaries/generate
func (h *Handler) generateSummary(c *gin.Context) {
	var dto generateSummaryDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	summary, err := h.generateSummaryNow(c.Request.Context(), dto.RefID, dto.Lang)
	if err != nil {
		if errors.Is(err, errSummaryArticleNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			response.NotFound(c)
			return
		}
		response.InternalError(c, err)
		return
	}
	response.OK(c, summary)
}

// GET /ai/summaries  [auth]
func (h *Handler) listSummaries(c *gin.Context) {
	q := pagination.FromContext(c)

	tx := h.svc.db.Model(&models.AISummaryModel{}).Order("created_at DESC")
	var items []models.AISummaryModel
	pag, err := pagination.Paginate(tx, q, &items)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	response.OK(c, gin.H{
		"data":       items,
		"pagination": pag,
		"articles":   h.findSummaryArticles(items),
	})
}

// GET /ai/summaries/ref/:id  [auth]
func (h *Handler) getSummariesByRefID(c *gin.Context) {
	refID := c.Param("id")
	article, ok, err := h.findSummaryArticle(refID)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if !ok {
		response.NotFound(c)
		return
	}

	var summaries []models.AISummaryModel
	if err := h.svc.db.Where("ref_id = ?", refID).Order("created_at DESC").Find(&summaries).Error; err != nil {
		response.InternalError(c, err)
		return
	}

	response.OK(c, gin.H{
		"summaries": summaries,
		"article":   article,
	})
}

// PATCH /ai/summaries/:id  [auth]
func (h *Handler) updateSummary(c *gin.Context) {
	var dto updateSummaryDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var item models.AISummaryModel
	if err := h.svc.db.First(&item, "id = ?", c.Param("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.NotFound(c)
			return
		}
		response.InternalError(c, err)
		return
	}

	item.Summary = dto.Summary
	if err := h.svc.db.Save(&item).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, item)
}

// DELETE /ai/summaries/:id  [auth]
func (h *Handler) deleteSummary(c *gin.Context) {
	result := h.svc.db.Delete(&models.AISummaryModel{}, "id = ?", c.Param("id"))
	if result.Error != nil {
		response.InternalError(c, result.Error)
		return
	}
	if result.RowsAffected == 0 {
		response.NotFound(c)
		return
	}
	response.NoContent(c)
}

func (h *Handler) generateSummaryNow(ctx context.Context, refID, lang string) (*models.AISummaryModel, error) {
	if lang == "" {
		cfg, _ := h.svc.cfgSvc.Get()
		if cfg != nil {
			lang = cfg.AI.AISummaryTargetLanguage
		}
	}
	if lang == "" {
		lang = "zh-CN"
	}

	if existing, err := h.svc.GetSummary(refID, lang); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	_, title, text := h.svc.fetchArticleInfo(refID)
	if text == "" {
		return nil, errSummaryArticleNotFound
	}

	cfg, err := h.svc.cfgSvc.Get()
	if err != nil {
		return nil, err
	}
	if cfg == nil || !cfg.AI.EnableSummary {
		return nil, errors.New("AI summary is disabled")
	}

	provider := selectAIProvider(cfg.AI, cfg.AI.SummaryModel)
	if provider == nil {
		return nil, errors.New("no enabled AI provider")
	}

	targetLang := lang
	if targetLang == "auto" {
		targetLang = "Chinese"
	}

	summaryText, err := callAI(provider, title, text, targetLang)
	if err != nil {
		return nil, err
	}

	hash := hashKey(refID, lang)
	model := models.AISummaryModel{
		Hash:    hash,
		Summary: summaryText,
		RefID:   refID,
		Lang:    lang,
	}
	if err := h.svc.db.Where("hash = ?", hash).Assign(model).FirstOrCreate(&model).Error; err != nil {
		return nil, err
	}
	return &model, nil
}

func (h *Handler) findSummaryArticles(summaries []models.AISummaryModel) map[string]gin.H {
	out := map[string]gin.H{}
	if len(summaries) == 0 {
		return out
	}

	refIDs := make([]string, 0, len(summaries))
	seen := make(map[string]struct{}, len(summaries))
	for _, item := range summaries {
		if _, ok := seen[item.RefID]; ok || item.RefID == "" {
			continue
		}
		seen[item.RefID] = struct{}{}
		refIDs = append(refIDs, item.RefID)
	}
	if len(refIDs) == 0 {
		return out
	}

	type articleLite struct {
		ID    string `gorm:"column:id"`
		Title string `gorm:"column:title"`
	}

	var posts []articleLite
	if err := h.svc.db.Model(&models.PostModel{}).Select("id, title").Where("id IN ?", refIDs).Find(&posts).Error; err == nil {
		for _, item := range posts {
			out[item.ID] = gin.H{"title": item.Title, "type": "posts", "id": item.ID}
		}
	}

	var notes []articleLite
	if err := h.svc.db.Model(&models.NoteModel{}).Select("id, title").Where("id IN ?", refIDs).Find(&notes).Error; err == nil {
		for _, item := range notes {
			out[item.ID] = gin.H{"title": item.Title, "type": "notes", "id": item.ID}
		}
	}

	var pages []articleLite
	if err := h.svc.db.Model(&models.PageModel{}).Select("id, title").Where("id IN ?", refIDs).Find(&pages).Error; err == nil {
		for _, item := range pages {
			out[item.ID] = gin.H{"title": item.Title, "type": "pages", "id": item.ID}
		}
	}

	return out
}

func (h *Handler) findSummaryArticle(refID string) (gin.H, bool, error) {
	type articleLite struct {
		ID    string `gorm:"column:id"`
		Title string `gorm:"column:title"`
	}

	var post articleLite
	if err := h.svc.db.Model(&models.PostModel{}).Select("id, title").First(&post, "id = ?", refID).Error; err == nil {
		return gin.H{
			"id":   post.ID,
			"type": "posts",
			"document": gin.H{
				"title": post.Title,
			},
		}, true, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}

	var note articleLite
	if err := h.svc.db.Model(&models.NoteModel{}).Select("id, title").First(&note, "id = ?", refID).Error; err == nil {
		return gin.H{
			"id":   note.ID,
			"type": "notes",
			"document": gin.H{
				"title": note.Title,
			},
		}, true, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}

	var page articleLite
	if err := h.svc.db.Model(&models.PageModel{}).Select("id, title").First(&page, "id = ?", refID).Error; err == nil {
		return gin.H{
			"id":   page.ID,
			"type": "pages",
			"document": gin.H{
				"title": page.Title,
			},
		}, true, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}

	return nil, false, nil
}

// GET /ai/models  [auth]
func (h *Handler) getAvailableModels(c *gin.Context) {
	cfg, err := h.svc.cfgSvc.Get()
	if err != nil {
		response.InternalError(c, err)
		return
	}

	out := make([]providerModelsResponse, 0, len(cfg.AI.Providers))
	for _, p := range cfg.AI.Providers {
		if !p.Enabled || p.APIKey == "" {
			continue
		}
		out = append(out, providerModelsResponse{
			ProviderID:   p.ID,
			ProviderName: p.Name,
			ProviderType: p.Type,
			Models:       modelsFromProvider(p),
		})
	}
	response.OK(c, out)
}

// GET /ai/models/:providerId  [auth]
func (h *Handler) getModelsForProvider(c *gin.Context) {
	providerID := c.Param("providerId")
	cfg, err := h.svc.cfgSvc.Get()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	for _, p := range cfg.AI.Providers {
		if p.ID == providerID {
			response.OK(c, providerModelsResponse{
				ProviderID:   p.ID,
				ProviderName: p.Name,
				ProviderType: p.Type,
				Models:       modelsFromProvider(p),
			})
			return
		}
	}
	response.NotFoundMsg(c, "provider not found")
}

// POST /ai/models/list  [auth]
func (h *Handler) fetchModelsList(c *gin.Context) {
	var dto fetchModelsDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Prefer explicit request payload; fallback to stored provider.
	provider := appcfg.AIProvider{
		ID:           dto.ProviderID,
		Name:         dto.ProviderID,
		Type:         dto.Type,
		APIKey:       dto.APIKey,
		Endpoint:     dto.Endpoint,
		DefaultModel: "",
		Enabled:      true,
	}

	if dto.ProviderID != "" {
		if cfg, err := h.svc.cfgSvc.Get(); err == nil {
			for _, p := range cfg.AI.Providers {
				if p.ID == dto.ProviderID {
					if provider.Type == "" {
						provider.Type = p.Type
					}
					if provider.APIKey == "" {
						provider.APIKey = p.APIKey
					}
					if provider.Endpoint == "" {
						provider.Endpoint = p.Endpoint
					}
					if provider.DefaultModel == "" {
						provider.DefaultModel = p.DefaultModel
					}
					if provider.Name == "" {
						provider.Name = p.Name
					}
					break
				}
			}
		}
	}

	if provider.Type == "" || provider.APIKey == "" {
		response.OK(c, gin.H{
			"models": []modelInfo{},
			"error":  "Provider type and api key are required",
		})
		return
	}

	response.OK(c, gin.H{
		"models": modelsFromProvider(provider),
	})
}

// POST /ai/test  [auth]
func (h *Handler) testProviderConnection(c *gin.Context) {
	var dto testConnectionDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if dto.ProviderID != "" && (dto.Type == "" || dto.APIKey == "" || dto.Model == "") {
		if cfg, err := h.svc.cfgSvc.Get(); err == nil {
			for _, p := range cfg.AI.Providers {
				if p.ID == dto.ProviderID {
					if dto.Type == "" {
						dto.Type = p.Type
					}
					if dto.APIKey == "" {
						dto.APIKey = p.APIKey
					}
					if dto.Model == "" {
						dto.Model = p.DefaultModel
					}
					if dto.Endpoint == "" {
						dto.Endpoint = p.Endpoint
					}
					break
				}
			}
		}
	}
	if dto.Type == "" || dto.APIKey == "" || dto.Model == "" {
		response.BadRequest(c, "type, apiKey and model are required")
		return
	}

	provider := appcfg.AIProvider{
		Type:         dto.Type,
		APIKey:       dto.APIKey,
		Endpoint:     dto.Endpoint,
		DefaultModel: dto.Model,
		Enabled:      true,
	}

	result, err := callAI(&provider, "Connection Test", "Say OK", "English")
	if err != nil {
		response.InternalError(c, err)
		return
	}
	_ = result
	response.OK(c, gin.H{"ok": true})
}

func selectAIProvider(cfg appcfg.AIConfig, assignment *appcfg.AIModelAssignment) *appcfg.AIProvider {
	var providerID string
	var overrideModel string
	if assignment != nil {
		providerID = strings.TrimSpace(assignment.ProviderID)
		overrideModel = strings.TrimSpace(assignment.Model)
	}

	pick := func(provider appcfg.AIProvider) *appcfg.AIProvider {
		selected := provider
		if overrideModel != "" {
			selected.DefaultModel = overrideModel
		}
		return &selected
	}

	if providerID != "" {
		for _, provider := range cfg.Providers {
			if !provider.Enabled {
				continue
			}
			if strings.TrimSpace(provider.ID) != providerID {
				continue
			}
			return pick(provider)
		}
	}

	for _, provider := range cfg.Providers {
		if !provider.Enabled {
			continue
		}
		return pick(provider)
	}

	return nil
}

func modelsFromProvider(provider appcfg.AIProvider) []modelInfo {
	models := make([]modelInfo, 0, 1)
	if provider.DefaultModel != "" {
		models = append(models, modelInfo{
			ID:   provider.DefaultModel,
			Name: provider.DefaultModel,
		})
	}
	return models
}

// GET /ai/deep-readings/article/:id
func (h *Handler) getDeepReading(c *gin.Context) {
	articleID := c.Param("id")
	dr, err := h.svc.GetDeepReading(articleID)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if dr == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, dr)
}

// GET /ai/tasks  [auth]
func (h *Handler) listTasks(c *gin.Context) {
	q := pagination.FromContext(c)
	taskType := c.Query("type")
	statusStr := c.Query("status")

	var taskTypePtr *string
	var statusPtr *taskqueue.TaskStatus

	if taskType != "" {
		taskTypePtr = &taskType
	}
	if statusStr != "" {
		s := taskqueue.TaskStatus(statusStr)
		statusPtr = &s
	}

	tasks, total, err := h.svc.taskSvc.List(c.Request.Context(), q.Page, q.Size, taskTypePtr, statusPtr)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	totalPages := int((total + int64(q.Size) - 1) / int64(q.Size))
	response.Paged(c, tasks, response.Pagination{
		Total:       total,
		CurrentPage: q.Page,
		TotalPage:   totalPages,
		Size:        q.Size,
		HasNextPage: q.Page < totalPages,
	})
}

// GET /ai/tasks/:id  [auth]
func (h *Handler) getTask(c *gin.Context) {
	task, err := h.svc.taskSvc.GetByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if task == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, task)
}

// DELETE /ai/tasks/:id  [auth]
func (h *Handler) deleteTask(c *gin.Context) {
	if err := h.svc.taskSvc.DeleteByID(c.Request.Context(), c.Param("id")); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.NoContent(c)
}

// DELETE /ai/tasks?before=<unix_ms>  [auth]
func (h *Handler) batchDeleteTasks(c *gin.Context) {
	beforeStr := c.Query("before")
	var before int64
	if beforeStr != "" {
		if v, err := strconv.ParseInt(beforeStr, 10, 64); err == nil {
			before = v
		}
	}
	if err := h.svc.taskSvc.DeleteCompleted(c.Request.Context(), before); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

// POST /ai/tasks/:id/cancel  [auth]
func (h *Handler) cancelTask(c *gin.Context) {
	if err := h.svc.taskSvc.Cancel(c.Request.Context(), c.Param("id")); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.NoContent(c)
}

// POST /ai/tasks/:id/retry  [auth]
func (h *Handler) retryTask(c *gin.Context) {
	task, err := h.svc.taskSvc.GetByID(c.Request.Context(), c.Param("id"))
	if err != nil || task == nil {
		response.NotFound(c)
		return
	}
	if task.Status != taskqueue.TaskFailed && task.Status != taskqueue.TaskCancelled {
		response.BadRequest(c, "only failed or cancelled tasks can be retried")
		return
	}

	var payload SummaryPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		response.BadRequest(c, "invalid task payload")
		return
	}

	newTask, err := h.svc.EnqueueSummary(c.Request.Context(), payload.RefID, payload.RefType, payload.Title, payload.Lang)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, newTask)
}

// POST /ai/summaries/task  [auth]
func (h *Handler) createSummaryTask(c *gin.Context) {
	var dto createSummaryTaskDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	refID := strings.TrimSpace(dto.RefID)
	if refID == "" {
		refID = strings.TrimSpace(dto.RefIDLegacy)
	}
	if refID == "" {
		response.BadRequest(c, "refId is required")
		return
	}

	task, err := h.svc.EnqueueSummary(c.Request.Context(), refID, "", "", strings.TrimSpace(dto.Lang))
	if err != nil {
		if errors.Is(err, errSummaryArticleNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			response.NotFound(c)
			return
		}
		response.InternalError(c, err)
		return
	}
	response.Created(c, task)
}

// GET /ai/summaries/task?ref_id=&lang=  [auth]
func (h *Handler) getSummaryTask(c *gin.Context) {
	refID := strings.TrimSpace(c.Query("ref_id"))
	lang := strings.TrimSpace(c.Query("lang"))
	if lang == "" {
		lang = "default"
	}
	if refID == "" {
		response.BadRequest(c, "ref_id is required")
		return
	}

	dedupKey := refID + ":" + lang
	tasks, _, err := h.svc.taskSvc.List(c.Request.Context(), 1, 100, strPtr(TaskTypeSummary), nil)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	for _, t := range tasks {
		if t.DedupKey == dedupKey {
			response.OK(c, t)
			return
		}
	}
	response.NotFound(c)
}

func strPtr(s string) *string { return &s }

// GET /ai/summaries/grouped  [auth]
func (h *Handler) getGroupedSummaries(c *gin.Context) {
	var summaries []models.AISummaryModel
	if err := h.svc.db.Order("created_at DESC").Find(&summaries).Error; err != nil {
		response.InternalError(c, err)
		return
	}

	grouped := map[string][]models.AISummaryModel{}
	for _, s := range summaries {
		grouped[s.RefID] = append(grouped[s.RefID], s)
	}
	response.OK(c, grouped)
}

// GET /ai/tasks/group/:groupKey  [auth]
func (h *Handler) getTasksByGroup(c *gin.Context) {
	groupKey := c.Param("groupKey")
	if groupKey == "" {
		groupKey = c.Param("id")
	}
	if groupKey == "" {
		response.BadRequest(c, "group id is required")
		return
	}
	q := pagination.FromContext(c)

	all, _, err := h.svc.taskSvc.List(c.Request.Context(), 1, 1000, nil, nil)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	var filtered []*taskqueue.Task
	for _, t := range all {
		if t.GroupKey == groupKey {
			filtered = append(filtered, t)
		}
	}

	total := int64(len(filtered))
	start := (q.Page - 1) * q.Size
	end := start + q.Size
	if start >= len(filtered) {
		filtered = []*taskqueue.Task{}
	} else {
		if end > len(filtered) {
			end = len(filtered)
		}
		filtered = filtered[start:end]
	}

	totalPages := int((total + int64(q.Size) - 1) / int64(q.Size))
	response.Paged(c, filtered, response.Pagination{
		Total:       total,
		CurrentPage: q.Page,
		TotalPage:   totalPages,
		Size:        q.Size,
		HasNextPage: q.Page < totalPages,
	})
}

// DELETE /ai/tasks/group/:groupKey  [auth]
func (h *Handler) cancelTasksByGroup(c *gin.Context) {
	groupKey := c.Param("groupKey")
	if groupKey == "" {
		groupKey = c.Param("id")
	}
	if groupKey == "" {
		response.BadRequest(c, "group id is required")
		return
	}

	all, _, err := h.svc.taskSvc.List(c.Request.Context(), 1, 1000, nil, nil)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	cancelled := 0
	for _, t := range all {
		if t.GroupKey != groupKey {
			continue
		}
		switch t.Status {
		case taskqueue.TaskPending:
			if err := h.svc.taskSvc.Cancel(c.Request.Context(), t.ID); err == nil {
				cancelled++
			}
		case taskqueue.TaskRunning:
			if err := h.svc.taskSvc.UpdateStatus(c.Request.Context(), t.ID, taskqueue.TaskCancelled, nil, "cancelled by group"); err == nil {
				cancelled++
			}
		}
	}

	response.OK(c, gin.H{"cancelled": cancelled})
}

// POST /ai/comment-review/test  [auth]
func (h *Handler) testCommentReview(c *gin.Context) {
	var dto struct {
		Text    string `json:"text"`
		Comment string `json:"comment"`
	}
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	text := strings.TrimSpace(dto.Text)
	if text == "" {
		text = strings.TrimSpace(dto.Comment)
	}
	if text == "" {
		response.BadRequest(c, "comment text is required")
		return
	}

	cfg, err := h.svc.cfgSvc.Get()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if cfg == nil || !cfg.CommentOptions.AIReview {
		response.BadRequest(c, "AI review is not enabled")
		return
	}

	provider := selectAIProvider(cfg.AI, cfg.AI.CommentReviewModel)
	if provider == nil || strings.TrimSpace(provider.APIKey) == "" {
		response.BadRequest(c, "no enabled AI provider")
		return
	}

	reviewType := strings.ToLower(strings.TrimSpace(cfg.CommentOptions.AIReviewType))
	if reviewType == "" {
		reviewType = "binary"
	}
	threshold := cfg.CommentOptions.AIReviewThreshold
	if threshold <= 0 {
		threshold = 5
	}

	if reviewType == "score" {
		raw, err := callAIWithPrompt(provider, fmt.Sprintf(
			"You are a content moderation specialist. Analyze the comment and return JSON only with keys \"score\" (1-10 number) and \"hasSensitiveContent\" (boolean).\nCOMMENT:\n%s",
			text,
		))
		if err != nil {
			response.InternalError(c, err)
			return
		}

		var output struct {
			Score               float64 `json:"score"`
			HasSensitiveContent bool    `json:"hasSensitiveContent"`
		}
		if err := unmarshalAIJSON(raw, &output); err != nil {
			response.InternalError(c, err)
			return
		}

		score := int(output.Score + 0.5)
		if score < 0 {
			score = 0
		}
		isSpam := score >= threshold || output.HasSensitiveContent
		reason := ""
		if output.HasSensitiveContent {
			reason = "contains sensitive content"
		} else if isSpam {
			reason = fmt.Sprintf("score %d exceeds threshold %d", score, threshold)
		}

		response.OK(c, gin.H{
			"isSpam": isSpam,
			"score":  score,
			"reason": reason,
		})
		return
	}

	raw, err := callAIWithPrompt(provider, fmt.Sprintf(
		"You are a spam detection specialist. Analyze the comment and return JSON only with keys \"isSpam\" (boolean) and \"hasSensitiveContent\" (boolean).\nCOMMENT:\n%s",
		text,
	))
	if err != nil {
		response.InternalError(c, err)
		return
	}

	var output struct {
		IsSpam              bool `json:"isSpam"`
		HasSensitiveContent bool `json:"hasSensitiveContent"`
	}
	if err := unmarshalAIJSON(raw, &output); err != nil {
		response.InternalError(c, err)
		return
	}

	isSpam := output.IsSpam || output.HasSensitiveContent
	reason := ""
	if output.HasSensitiveContent {
		reason = "contains sensitive content"
	} else if output.IsSpam {
		reason = "classified as spam"
	}

	response.OK(c, gin.H{
		"isSpam": isSpam,
		"reason": reason,
	})
}
