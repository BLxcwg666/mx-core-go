package configs

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const configKey = "configs"

//go:embed form_schema.template.json
var formSchemaTemplateRaw []byte

//go:embed email-template/owner.template.ejs
var ownerTemplateRaw string

//go:embed email-template/guest.template.ejs
var guestTemplateRaw string

//go:embed email-template/newsletter.template.ejs
var newsletterTemplateRaw string

var providerNameUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var (
	formSchemaLoadOnce sync.Once
	formSchemaTemplate map[string]interface{}
	formSchemaLoadErr  error
)

type providerSelectOption struct {
	Label string
	Value string
}

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
	merged := map[string]interface{}{}
	if err := json.Unmarshal(currentJSON, &merged); err != nil {
		return nil, err
	}

	for k, v := range partial {
		if len(strings.TrimSpace(string(v))) == 0 {
			continue
		}
		var incoming interface{}
		if err := json.Unmarshal(v, &incoming); err != nil {
			return nil, err
		}
		if existing, ok := merged[k]; ok {
			merged[k] = deepMergeJSON(existing, incoming)
			continue
		}
		merged[k] = incoming
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

func deepMergeJSON(oldVal, newVal interface{}) interface{} {
	oldMap, oldIsMap := oldVal.(map[string]interface{})
	newMap, newIsMap := newVal.(map[string]interface{})
	if oldIsMap && newIsMap {
		out := make(map[string]interface{}, len(oldMap))
		for k, v := range oldMap {
			out[k] = v
		}
		for k, v := range newMap {
			if existing, ok := out[k]; ok {
				out[k] = deepMergeJSON(existing, v)
				continue
			}
			out[k] = v
		}
		return out
	}

	// Arrays should be replaced as a whole.
	if _, ok := newVal.([]interface{}); ok {
		return newVal
	}

	return newVal
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
			"text":             "年纪在四十以上，二十以下的，恐怕就不易在前两派里有个地位了。他们的车破，又不敢“拉晚儿”……",
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
					"text":    "年纪在四十以上，二十以下的，恐怕就不易在前两派里有个地位了。他们的车破，又不敢“拉晚儿”……",
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

func loadFormSchemaTemplate() (map[string]interface{}, error) {
	formSchemaLoadOnce.Do(func() {
		decoded := decodeJSONBytes(formSchemaTemplateRaw)
		if err := json.Unmarshal(decoded, &formSchemaTemplate); err != nil {
			formSchemaLoadErr = err
		}
	})
	if formSchemaLoadErr != nil {
		return nil, formSchemaLoadErr
	}

	raw, err := json.Marshal(formSchemaTemplate)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeJSONBytes(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}

	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})

	if len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE {
		return utf16ToUTF8(raw[2:], true)
	}

	if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF {
		return utf16ToUTF8(raw[2:], false)
	}

	return raw
}

func utf16ToUTF8(raw []byte, littleEndian bool) []byte {
	if len(raw) < 2 {
		return []byte{}
	}
	u16 := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		if littleEndian {
			u16 = append(u16, uint16(raw[i])|uint16(raw[i+1])<<8)
			continue
		}
		u16 = append(u16, uint16(raw[i])<<8|uint16(raw[i+1]))
	}
	return []byte(string(utf16.Decode(u16)))
}

func attachAIProviderOptions(schema map[string]interface{}, aiCfg config.AIConfig) {
	options := make([]providerSelectOption, 0, len(aiCfg.Providers))
	seen := map[string]struct{}{}

	addOption := func(id, label string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		if strings.TrimSpace(label) == "" {
			label = id
		}
		options = append(options, providerSelectOption{Label: label, Value: id})
	}

	for _, provider := range aiCfg.Providers {
		addOption(provider.ID, formatAIProviderLabel(provider))
	}

	if aiCfg.SummaryModel != nil {
		addOption(aiCfg.SummaryModel.ProviderID, aiCfg.SummaryModel.ProviderID)
	}
	if aiCfg.CommentReviewModel != nil {
		addOption(aiCfg.CommentReviewModel.ProviderID, aiCfg.CommentReviewModel.ProviderID)
	}
	if len(options) == 0 {
		return
	}

	groups, ok := schema["groups"].([]interface{})
	if !ok {
		return
	}

	for _, group := range groups {
		groupMap, ok := group.(map[string]interface{})
		if !ok {
			continue
		}
		if strings.TrimSpace(fmt.Sprintf("%v", groupMap["key"])) != "ai" {
			continue
		}

		sections, ok := groupMap["sections"].([]interface{})
		if !ok {
			continue
		}
		for _, section := range sections {
			sectionMap, ok := section.(map[string]interface{})
			if !ok {
				continue
			}
			if strings.TrimSpace(fmt.Sprintf("%v", sectionMap["key"])) != "ai" {
				continue
			}

			fields, ok := sectionMap["fields"].([]interface{})
			if !ok {
				continue
			}
			attachAIProviderOptionsToFields(fields, options)
		}
	}
}

func attachAIProviderOptionsToFields(fields []interface{}, options []providerSelectOption) {
	for _, field := range fields {
		fieldMap, ok := field.(map[string]interface{})
		if !ok {
			continue
		}

		if strings.TrimSpace(fmt.Sprintf("%v", fieldMap["key"])) == "providerId" &&
			strings.TrimSpace(fmt.Sprintf("%v", fieldMap["title"])) == "Provider ID" {
			ui, _ := fieldMap["ui"].(map[string]interface{})
			if ui == nil {
				ui = map[string]interface{}{}
				fieldMap["ui"] = ui
			}

			ui["component"] = "select"
			selectOptions := make([]map[string]interface{}, 0, len(options))
			for _, option := range options {
				selectOptions = append(selectOptions, map[string]interface{}{
					"label": option.Label,
					"value": option.Value,
				})
			}
			ui["options"] = selectOptions
		}

		nestedFields, ok := fieldMap["fields"].([]interface{})
		if !ok {
			continue
		}
		attachAIProviderOptionsToFields(nestedFields, options)
	}
}

func formatAIProviderLabel(provider config.AIProvider) string {
	name := strings.TrimSpace(provider.Name)
	providerType := strings.TrimSpace(provider.Type)
	id := strings.TrimSpace(provider.ID)

	displayName := name
	if providerNameUUIDPattern.MatchString(displayName) {
		displayName = ""
	}

	if displayName != "" && providerType != "" {
		return fmt.Sprintf("%s (%s)", displayName, providerType)
	}
	if displayName != "" {
		return displayName
	}
	if providerType != "" {
		return providerType
	}
	if id != "" {
		return id
	}
	return "Unknown"
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
	parts := strings.Split(s, "_")
	if len(parts) == 1 {
		return s
	}
	out := make([]rune, 0, len(s))
	out = append(out, []rune(parts[0])...)
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lower := strings.ToLower(part)
		switch lower {
		case "mb":
			out = append(out, []rune("MB")...)
			continue
		case "ttl":
			out = append(out, []rune("TTL")...)
			continue
		}
		runes := []rune(lower)
		runes[0] = unicode.ToUpper(runes[0])
		out = append(out, runes...)
	}
	return string(out)
}

func camelToSnakeKey(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(strings.TrimSpace(s))
	if len(runes) == 0 {
		return ""
	}
	out := make([]rune, 0, len(runes)+4)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || nextLower {
					out = append(out, '_')
				}
			}
			out = append(out, unicode.ToLower(r))
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
