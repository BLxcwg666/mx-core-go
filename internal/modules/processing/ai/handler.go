package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	appcfg "github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

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
		response.NotFoundMsg(c, "翻译不存在")
		return
	}

	task, err := h.svc.EnqueueSummary(c.Request.Context(), articleID, "", "", lang)
	if err != nil {
		if errors.Is(err, errSummaryArticleNotFound) {
			response.NotFoundMsg(c, "文章不存在")
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

// GET /ai/summaries/article/:id/generate  — SSE streaming
func (h *Handler) streamSummaryGenerate(c *gin.Context) {
	articleID := c.Param("id")
	lang := c.DefaultQuery("lang", "zh-CN")
	h.svc.GenerateSummaryStream(c, articleID, lang)
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
			response.NotFoundMsg(c, "文章不存在")
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
		response.NotFoundMsg(c, "文章不存在")
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
			response.NotFoundMsg(c, "翻译不存在")
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
		response.NotFoundMsg(c, "翻译不存在")
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

	summaryText, err := callAI(provider, title, text, lang)
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
	response.NotFoundMsg(c, "AI Provider 不存在")
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

	fetchedModels, err := fetchModelsFromProvider(provider)
	if err != nil {
		fallback := modelsFromProvider(provider)
		response.OK(c, gin.H{
			"models": fallback,
			"error":  err.Error(),
		})
		return
	}
	if len(fetchedModels) == 0 {
		fetchedModels = modelsFromProvider(provider)
	}

	response.OK(c, gin.H{
		"models": fetchedModels,
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

// GET /ai/deep-readings/article/:id
func (h *Handler) getDeepReading(c *gin.Context) {
	articleID := c.Param("id")
	dr, err := h.svc.GetDeepReading(articleID)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if dr == nil {
		response.NotFoundMsg(c, "内容不存在")
		return
	}
	response.OK(c, dr)
}

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
		response.BadRequest(c, "AI 评论审核未开启")
		return
	}

	provider := selectAIProvider(cfg.AI, cfg.AI.CommentReviewModel)
	if provider == nil || strings.TrimSpace(provider.APIKey) == "" {
		response.BadRequest(c, "没有配置启用的 AI Provider")
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
		systemPrompt, prompt := buildCommentScorePrompt(text)
		raw, err := callAIWithSystemPrompt(provider, systemPrompt, prompt)
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
		isSpam := score > threshold || output.HasSensitiveContent
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

	systemPrompt, prompt := buildCommentSpamPrompt(text)
	raw, err := callAIWithSystemPrompt(provider, systemPrompt, prompt)
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
