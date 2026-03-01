package configs

import (
	"encoding/json"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/response"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/configs")

	g.GET("", h.getPublic)

	a := g.Group("", authMW)
	a.GET("/all", h.getAll)
	a.PATCH("", h.patch)

	// /options/:key - used by admin panel (e.g. PATCH /options/oauth)
	opts := rg.Group("/options", authMW)
	opts.GET("", h.getOptionsAll)
	opts.GET("/email/template", h.getEmailTemplate)
	opts.GET("/:key", h.getOption)
	opts.PATCH("/:key", h.patchOption)
	opts.PUT("/email/template", h.putEmailTemplate)
	opts.DELETE("/email/template", h.deleteEmailTemplate)
	cfgLegacy := rg.Group("/config", authMW)
	cfgLegacy.GET("/form-schema", h.getFormSchema)
}

// getPublic returns the public-safe subset of the config.
func (h *Handler) getPublic(c *gin.Context) {
	cfg, err := h.svc.Get()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, gin.H{
		"seo":                 cfg.SEO,
		"url":                 cfg.URL,
		"friend_link_options": cfg.FriendLinkOptions,
		"feature_list":        cfg.FeatureList,
		"admin_extra":         cfg.AdminExtra,
	})
}

// getAll returns the full config (admin only). Sensitive fields like API keys are included.
func (h *Handler) getAll(c *gin.Context) {
	cfg, err := h.svc.Get()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, cfg)
}

// patch merges a partial config update.
func (h *Handler) patch(c *gin.Context) {
	var partial map[string]json.RawMessage
	if err := c.ShouldBindJSON(&partial); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	updated, err := h.svc.Patch(partial)
	if err != nil {
		if errors.Is(err, errAIReviewProviderNotEnabled) {
			response.BadRequest(c, "没有配置启用的 AI Provider，无法启用 AI 评论审核")
			return
		}
		response.InternalError(c, err)
		return
	}
	response.OK(c, updated)
}

// getOption returns a specific top-level config key (e.g. GET /options/oauth).
func (h *Handler) getOption(c *gin.Context) {
	key := normalizeOptionKey(c.Param("key"))
	cfg, err := h.svc.Get()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	// Re-marshal and pick the key
	full, _ := json.Marshal(cfg)
	var m map[string]json.RawMessage
	json.Unmarshal(full, &m)
	if val, ok := m[key]; ok {
		var result interface{}
		json.Unmarshal(val, &result)
		response.OK(c, convertMapKeys(result, snakeToCamelKey))
		return
	}
	response.NotFound(c)
}

// patchOption merges an update into a specific top-level config key (e.g. PATCH /options/oauth).
func (h *Handler) patchOption(c *gin.Context) {
	key := normalizeOptionKey(c.Param("key"))
	var body json.RawMessage
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	normalizedBody, err := normalizeJSONKeys(body, camelToSnakeKey)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	updated, err := h.svc.Patch(map[string]json.RawMessage{key: normalizedBody})
	if err != nil {
		if errors.Is(err, errAIReviewProviderNotEnabled) {
			response.BadRequest(c, "没有配置启用的 AI Provider，无法启用 AI 评论审核")
			return
		}
		response.InternalError(c, err)
		return
	}

	full, _ := json.Marshal(updated)
	var m map[string]json.RawMessage
	json.Unmarshal(full, &m)
	if val, ok := m[key]; ok {
		var result interface{}
		json.Unmarshal(val, &result)
		response.OK(c, convertMapKeys(result, snakeToCamelKey))
		return
	}
	response.OK(c, convertMapKeys(updated, snakeToCamelKey))
}

func (h *Handler) getOptionsAll(c *gin.Context) {
	cfg, err := h.svc.Get()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, convertMapKeys(cfg, snakeToCamelKey))
}

func (h *Handler) getFormSchema(c *gin.Context) {
	schema, err := loadFormSchemaTemplate()
	if err != nil {
		response.InternalError(c, err)
		return
	}

	cfg, err := h.svc.Get()
	if err != nil {
		response.InternalError(c, err)
		return
	}

	schema["defaults"] = convertMapKeys(config.DefaultFullConfig(), snakeToCamelKey)
	attachAIProviderOptions(schema, cfg.AI)
	response.OK(c, schema)
}

// defaultEmailTemplates returns the built-in EJS template and example render props for each type.
var defaultEmailTemplates = map[string]struct {
	Template string
	Props    interface{}
}{
	"owner": {
		Template: ownerTemplateRaw,
		Props: map[string]interface{}{
			"author":  "Commentor",
			"avatar":  "https://cloudflare-ipfs.com/ipfs/Qmd3W5DuhgHirLHGVixi6V76LhCkZUz6pnFt5AJBiyvHye/avatar/976.jpg",
			"mail":    "commtor@example.com",
			"text":    "世界！",
			"ip":      "0.0.0.0",
			"agent":   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
			"created": "2024-01-01T00:00:00.000Z",
			"url":     "https://blog.commentor.com",
			"link":    "https://innei.in/note/122#comments-37ccbeec9c15bb0ddc51ca7d",
			"time":    "2024/01/01",
			"title":   "匆匆",
			"master":  "innei",
			"aggregate": map[string]interface{}{
				"post": map[string]interface{}{
					"title":    "匆匆",
					"id":       "d7e0ed429da8ae90988c37da",
					"text":     "燕子去了，有再来的时候；杨柳枯了，有再青的时候；桃花谢了，有再开的时候。",
					"created":  "2024-01-01T00:00:00.000Z",
					"modified": nil,
				},
				"commentor": map[string]interface{}{
					"author":     "Commentor",
					"avatar":     "https://cloudflare-ipfs.com/ipfs/Qmd3W5DuhgHirLHGVixi6V76LhCkZUz6pnFt5AJBiyvHye/avatar/976.jpg",
					"mail":       "commtor@example.com",
					"text":       "世界！",
					"ip":         "0.0.0.0",
					"agent":      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
					"created":    "2024-01-01T00:00:00.000Z",
					"isWhispers": false,
					"location":   "",
					"url":        "https://blog.commentor.com",
				},
				"parent": map[string]interface{}{
					"text": "你之前的评论内容",
				},
				"owner": map[string]interface{}{
					"name":   "innei",
					"avatar": "https://cdn.jsdelivr.net/gh/mx-space/.github@main/uwu.png",
					"mail":   "master@example.com",
					"url":    "https://innei.in",
				},
			},
		},
	},
	"guest": {
		Template: guestTemplateRaw,
		Props: map[string]interface{}{
			"author": "innei",
			"mail":   "commtor@example.com",
			"text":   "给你的回复内容",
			"ip":     "0.0.0.0",
			"link":   "https://innei.in/note/122#comments-37ccbeec9c15bb0ddc51ca7d",
			"time":   "2024/01/01",
			"title":  "匆匆",
			"master": "innei",
			"aggregate": map[string]interface{}{
				"parent": map[string]interface{}{
					"text": "你之前的回复内容",
				},
				"owner": map[string]interface{}{
					"name":   "innei",
					"avatar": "https://cdn.jsdelivr.net/gh/mx-space/.github@main/uwu.png",
					"mail":   "master@example.com",
					"url":    "https://innei.in",
				},
			},
		},
	},
	"newsletter": {
		Template: newsletterTemplateRaw,
		Props: map[string]interface{}{
			"text":             "年纪在四十以上，二十以下的，恐怕就不易在前两派里有个地位了。他们的车破，又不敢\u201c拉晚儿\u201d……",
			"title":            "骆驼祥子",
			"author":           "innei",
			"detail_link":      "#detail_link",
			"unsubscribe_link": "#unsubscribe_link",
			"master":           "innei",
			"aggregate": map[string]interface{}{
				"owner": map[string]interface{}{
					"name":   "innei",
					"avatar": "https://cdn.jsdelivr.net/gh/mx-space/.github@main/uwu.png",
				},
				"subscriber": map[string]interface{}{
					"email":     "subscriber@mail.com",
					"subscribe": 15,
				},
				"post": map[string]interface{}{
					"text":    "年纪在四十以上，二十以下的，恐怕就不易在前两派里有个地位了。他们的车破，又不敢\u201c拉晚儿\u201d……",
					"title":   "骆驼祥子",
					"id":      "cdab54a19f3f03f7f5159df7",
					"created": "2023-06-04T15:02:09.179Z",
				},
			},
		},
	},
}

const emailTemplateKeyPrefix = "email_template_"

// GET /options/email/template?type=owner|guest|newsletter
func (h *Handler) getEmailTemplate(c *gin.Context) {
	templateType := c.Query("type")
	def, ok := defaultEmailTemplates[templateType]
	if !ok {
		response.BadRequest(c, "invalid type, must be owner|guest|newsletter")
		return
	}

	var opt models.OptionModel
	err := h.svc.db.Where("name = ?", emailTemplateKeyPrefix+templateType).First(&opt).Error
	templateStr := def.Template
	if err == nil && opt.Value != "" {
		templateStr = opt.Value
	}

	response.OK(c, gin.H{"template": templateStr, "props": def.Props})
}

// PUT /options/email/template?type=...  body: {source: string}
func (h *Handler) putEmailTemplate(c *gin.Context) {
	templateType := c.Query("type")
	if _, ok := defaultEmailTemplates[templateType]; !ok {
		response.BadRequest(c, "invalid type")
		return
	}
	var body struct {
		Source string `json:"source" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	opt := models.OptionModel{Name: emailTemplateKeyPrefix + templateType, Value: body.Source}
	err := h.svc.db.Where("name = ?", opt.Name).Assign(opt).FirstOrCreate(&opt).Error
	if err != nil {
		// fallback: update
		h.svc.db.Model(&opt).Update("value", body.Source)
	}
	response.OK(c, gin.H{"source": body.Source})
}

// DELETE /options/email/template?type=... → reset to default
func (h *Handler) deleteEmailTemplate(c *gin.Context) {
	templateType := c.Query("type")
	if _, ok := defaultEmailTemplates[templateType]; !ok {
		response.BadRequest(c, "invalid type")
		return
	}
	h.svc.db.Where("name = ?", emailTemplateKeyPrefix+templateType).Delete(&models.OptionModel{})
	response.NoContent(c)
}
