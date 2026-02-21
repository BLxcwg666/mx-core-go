package configs

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const configKey = "configs"

// Service manages the persisted FullConfig.
type Service struct {
	db  *gorm.DB
	mu  sync.RWMutex
	cfg *config.FullConfig
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

// Get returns the current config, loading from DB if not cached.
func (s *Service) Get() (*config.FullConfig, error) {
	s.mu.RLock()
	if s.cfg != nil {
		defer s.mu.RUnlock()
		return s.cfg, nil
	}
	s.mu.RUnlock()

	return s.load()
}

func (s *Service) load() (*config.FullConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var opt models.OptionModel
	err := s.db.Where("name = ?", configKey).First(&opt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		defaults := config.DefaultFullConfig()
		s.cfg = &defaults
		_ = s.persist(&defaults)
		return s.cfg, nil
	}
	if err != nil {
		return nil, err
	}

	cfg := config.DefaultFullConfig()
	if err := json.Unmarshal([]byte(opt.Value), &cfg); err != nil {
		return nil, err
	}
	s.cfg = &cfg
	return s.cfg, nil
}

// Patch merges the given partial JSON update into the current config and persists it.
func (s *Service) Patch(partial map[string]json.RawMessage) (*config.FullConfig, error) {
	current, err := s.Get()
	if err != nil {
		return nil, err
	}

	currentJSON, err := json.Marshal(current)
	if err != nil {
		return nil, err
	}
	merged := map[string]json.RawMessage{}
	if err := json.Unmarshal(currentJSON, &merged); err != nil {
		return nil, err
	}
	for k, v := range partial {
		merged[k] = v
	}
	mergedJSON, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}

	updated := config.DefaultFullConfig()
	if err := json.Unmarshal(mergedJSON, &updated); err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cfg = &updated
	s.mu.Unlock()

	return &updated, s.persist(&updated)
}

func (s *Service) persist(cfg *config.FullConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	opt := models.OptionModel{Name: configKey, Value: string(data)}
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&opt).Error
}

// Invalidate clears the in-memory config cache, forcing a DB reload on next Get.
func (s *Service) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = nil
}

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
	defaults := convertMapKeys(config.DefaultFullConfig(), snakeToCamelKey)
	response.OK(c, gin.H{
		"title":       "System Config",
		"description": "System configuration schema",
		"groups": []interface{}{
			gin.H{
				"key":         "site",
				"title":       "Site",
				"description": "Basic site settings",
				"icon":        "settings",
				"sections": []interface{}{
					gin.H{
						"key":   "seo",
						"title": "SEO",
						"fields": []interface{}{
							buildField("title", "Site Title", "input", true),
							buildField("description", "Description", "textarea", false),
							buildField("keywords", "Keywords", "tags", false),
						},
					},
					gin.H{
						"key":   "url",
						"title": "URL",
						"fields": []interface{}{
							buildField("webUrl", "Web URL", "input", true),
							buildField("adminUrl", "Admin URL", "input", false),
							buildField("serverUrl", "Server URL", "input", false),
							buildField("wsUrl", "WS URL", "input", false),
						},
					},
				},
			},
			gin.H{
				"key":         "system",
				"title":       "System",
				"description": "System and integration settings",
				"icon":        "plug",
				"sections": []interface{}{
					gin.H{
						"key":   "mailOptions",
						"title": "Mail",
						"fields": []interface{}{
							buildField("enable", "Enabled", "switch", false),
							buildField("provider", "Provider", "select", false, []interface{}{
								gin.H{"label": "smtp", "value": "smtp"},
								gin.H{"label": "resend", "value": "resend"},
							}),
							buildField("from", "From", "input", false),
						},
					},
					gin.H{
						"key":   "adminExtra",
						"title": "Admin",
						"fields": []interface{}{
							buildField("enableAdminProxy", "Enable Admin Proxy", "switch", false),
							buildField("background", "Login Background", "input", false),
							buildField("walineServerUrl", "Waline URL", "input", false),
						},
					},
					gin.H{
						"key":   "authSecurity",
						"title": "Auth",
						"fields": []interface{}{
							buildField("disablePasswordLogin", "Disable Password Login", "switch", false),
						},
					},
					gin.H{
						"key":   "featureList",
						"title": "Features",
						"fields": []interface{}{
							buildField("friendlyCommentEditorEnabled", "Friendly Comment Editor", "switch", false),
						},
					},
					gin.H{
						"key":    "ai",
						"title":  "AI",
						"fields": []interface{}{},
					},
				},
			},
		},
		"defaults": defaults,
	})
}

// defaultEmailTemplates returns the built-in EJS template and example render props for each type.
var defaultEmailTemplates = map[string]struct {
	Template string
	Props    interface{}
}{
	"owner": {
		Template: `<div>
  <h2>New Comment Notification</h2>
  <p>Your post <strong><%= title %></strong> has a new comment:</p>
  <blockquote><%= comment %></blockquote>
  <p>Author: <%= author %></p>
</div>`,
		Props: map[string]string{"title": "My Post", "comment": "Great article!", "author": "Guest"},
	},
	"guest": {
		Template: `<div>
  <h2>Author Replied</h2>
  <p>Your comment on <strong><%= title %></strong> has a new reply:</p>
  <blockquote><%= reply %></blockquote>
</div>`,
		Props: map[string]string{"title": "My Post", "reply": "Thanks for your feedback!"},
	},
	"newsletter": {
		Template: `<div>
  <h2>Subscription Confirmation</h2>
  <p>Please click the link below to confirm your subscription:</p>
  <a href="<%= url %>">Confirm Subscription</a>
</div>`,
		Props: map[string]string{"url": "https://example.com/subscribe/verify?token=xxx"},
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

// DELETE /options/email/template?type=... 闁?reset to default
func (h *Handler) deleteEmailTemplate(c *gin.Context) {
	templateType := c.Query("type")
	if _, ok := defaultEmailTemplates[templateType]; !ok {
		response.BadRequest(c, "invalid type")
		return
	}
	h.svc.db.Where("name = ?", emailTemplateKeyPrefix+templateType).Delete(&models.OptionModel{})
	response.NoContent(c)
}

func buildField(key, title, component string, required bool, options ...[]interface{}) gin.H {
	ui := gin.H{"component": component}
	if len(options) > 0 && len(options[0]) > 0 {
		ui["options"] = options[0]
	}
	return gin.H{
		"key":      key,
		"title":    title,
		"required": required,
		"ui":       ui,
	}
}

func normalizeOptionKey(key string) string {
	snake := camelToSnakeKey(key)
	if _, ok := optionKeyAliases[snake]; ok {
		return snake
	}
	return snake
}

var optionKeyAliases = map[string]string{
	"seo":                             "seo",
	"url":                             "url",
	"mail_options":                    "mail_options",
	"comment_options":                 "comment_options",
	"backup_options":                  "backup_options",
	"baidu_search_options":            "baidu_search_options",
	"algolia_search_options":          "algolia_search_options",
	"admin_extra":                     "admin_extra",
	"friend_link_options":             "friend_link_options",
	"s3_options":                      "s3_options",
	"image_bed_options":               "image_bed_options",
	"image_storage_options":           "image_storage_options",
	"third_party_service_integration": "third_party_service_integration",
	"text_options":                    "text_options",
	"bing_search_options":             "bing_search_options",
	"meili_search_options":            "meili_search_options",
	"feature_list":                    "feature_list",
	"bark_options":                    "bark_options",
	"auth_security":                   "auth_security",
	"ai":                              "ai",
	"oauth":                           "oauth",
}

func normalizeJSONKeys(raw json.RawMessage, keyFn func(string) string) (json.RawMessage, error) {
	var data interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("invalid json body")
	}
	normalized := convertMapKeys(data, keyFn)
	out, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func convertMapKeys(v interface{}, keyFn func(string) string) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, child := range val {
			out[keyFn(k)] = convertMapKeys(child, keyFn)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, child := range val {
			out[i] = convertMapKeys(child, keyFn)
		}
		return out
	case *config.FullConfig:
		if val == nil {
			return nil
		}
		b, _ := json.Marshal(val)
		var m map[string]interface{}
		_ = json.Unmarshal(b, &m)
		return convertMapKeys(m, keyFn)
	case config.FullConfig:
		b, _ := json.Marshal(val)
		var m map[string]interface{}
		_ = json.Unmarshal(b, &m)
		return convertMapKeys(m, keyFn)
	default:
		return val
	}
}

func snakeToCamelKey(s string) string {
	if s == "" {
		return s
	}
	out := make([]rune, 0, len(s))
	upperNext := false
	for _, r := range s {
		if r == '_' {
			upperNext = true
			continue
		}
		if upperNext {
			out = append(out, unicode.ToUpper(r))
			upperNext = false
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

func camelToSnakeKey(s string) string {
	if s == "" {
		return s
	}
	out := make([]rune, 0, len(s)+4)
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				out = append(out, '_')
			}
			out = append(out, unicode.ToLower(r))
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
