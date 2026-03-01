package metapreset

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type Handler struct {
	db *gorm.DB
}

func NewHandler(db *gorm.DB) *Handler { return &Handler{db: db} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/meta-presets")
	g.GET("", h.list)
	g.GET("/:id", h.getByID)

	a := g.Group("", authMW)
	a.POST("", h.create)
	a.PATCH("/:id", h.update)
	a.DELETE("/:id", h.delete)
	a.PUT("/order", h.updateOrder)
}

var seedBuiltinsOnce sync.Once

func (h *Handler) ensureBuiltins() {
	seedBuiltinsOnce.Do(func() {
		builtins := []models.MetaPresetModel{
			{
				Key:               "aiGen",
				Label:             "AI 参与声明",
				Type:              "checkbox",
				Scope:             "both",
				Description:       "声明 AI 在创作过程中的参与程度",
				AllowCustomOption: true,
				Options: []models.MetaFieldOption{
					{Value: -1, Label: "无 AI (手作)", Exclusive: true},
					{Value: 0, Label: "辅助写作"},
					{Value: 1, Label: "润色"},
					{Value: 2, Label: "完全 AI 生成", Exclusive: true},
					{Value: 3, Label: "故事整理"},
					{Value: 4, Label: "标题生成"},
					{Value: 5, Label: "校对"},
					{Value: 6, Label: "灵感提供"},
					{Value: 7, Label: "改写"},
					{Value: 8, Label: "AI 作图"},
					{Value: 9, Label: "口述"},
				},
				IsBuiltin: true,
				Order:     0,
				Enabled:   true,
			},
			{
				Key:         "cover",
				Label:       "封面图",
				Type:        "url",
				Scope:       "both",
				Placeholder: "https://...",
				IsBuiltin:   true,
				Order:       1,
				Enabled:     true,
			},
			{
				Key:         "banner",
				Label:       "横幅信息",
				Type:        "object",
				Scope:       "both",
				Description: "在文章顶部显示的提示横幅",
				Children: []models.MetaPresetChild{
					{
						Key:   "type",
						Label: "类型",
						Type:  "select",
						Options: []models.MetaFieldOption{
							{Value: "info", Label: "信息"},
							{Value: "warning", Label: "警告"},
							{Value: "error", Label: "错误"},
							{Value: "success", Label: "成功"},
							{Value: "secondary", Label: "次要"},
						},
					},
					{
						Key:   "message",
						Label: "消息内容",
						Type:  "textarea",
					},
					{
						Key:         "className",
						Label:       "自定义类名",
						Type:        "text",
						Placeholder: "可选的 CSS 类名",
					},
				},
				IsBuiltin: true,
				Order:     2,
				Enabled:   true,
			},
			{
				Key:         "keywords",
				Label:       "SEO 关键词",
				Type:        "tags",
				Scope:       "both",
				Placeholder: "输入关键词后按回车",
				IsBuiltin:   true,
				Order:       3,
				Enabled:     true,
			},
			{
				Key:         "style",
				Label:       "文章样式",
				Type:        "text",
				Scope:       "both",
				Placeholder: "输入样式名称",
				IsBuiltin:   true,
				Order:       4,
				Enabled:     true,
			},
		}

		for _, preset := range builtins {
			var existing models.MetaPresetModel
			err := h.db.Where("`key` = ?", preset.Key).First(&existing).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					if createErr := h.db.Create(&preset).Error; createErr != nil {
						// If key already exists due legacy/dirty data, upgrade that record to builtin.
						_ = h.db.Where("`key` = ?", preset.Key).Updates(map[string]interface{}{
							"label":               preset.Label,
							"type":                preset.Type,
							"scope":               preset.Scope,
							"description":         preset.Description,
							"placeholder":         preset.Placeholder,
							"options":             marshalJSONColumn(preset.Options),
							"allow_custom_option": preset.AllowCustomOption,
							"children":            marshalJSONColumn(preset.Children),
							"is_builtin":          true,
						}).Error
					}
				}
				continue
			}
			_ = h.db.Model(&existing).Updates(map[string]interface{}{
				"label":               preset.Label,
				"type":                preset.Type,
				"scope":               preset.Scope,
				"description":         preset.Description,
				"placeholder":         preset.Placeholder,
				"options":             marshalJSONColumn(preset.Options),
				"allow_custom_option": preset.AllowCustomOption,
				"children":            marshalJSONColumn(preset.Children),
				"is_builtin":          true,
			}).Error
		}
	})
}

type createMetaPresetDTO struct {
	Key               string                   `json:"key" binding:"required"`
	Label             string                   `json:"label" binding:"required"`
	Type              string                   `json:"type" binding:"required"`
	Description       string                   `json:"description"`
	Placeholder       string                   `json:"placeholder"`
	Scope             string                   `json:"scope"`
	Options           []models.MetaFieldOption `json:"options"`
	AllowCustomOption bool                     `json:"allowCustomOption"`
	Children          []models.MetaPresetChild `json:"children"`
	Order             *int                     `json:"order"`
	Enabled           *bool                    `json:"enabled"`
}

type updateMetaPresetDTO struct {
	Key               *string                   `json:"key"`
	Label             *string                   `json:"label"`
	Type              *string                   `json:"type"`
	Description       *string                   `json:"description"`
	Placeholder       *string                   `json:"placeholder"`
	Scope             *string                   `json:"scope"`
	Options           *[]models.MetaFieldOption `json:"options"`
	AllowCustomOption *bool                     `json:"allowCustomOption"`
	Children          *[]models.MetaPresetChild `json:"children"`
	Order             *int                      `json:"order"`
	Enabled           *bool                     `json:"enabled"`
}

type updateOrderDTO struct {
	IDs []string `json:"ids" binding:"required"`
}

func (h *Handler) list(c *gin.Context) {
	h.ensureBuiltins()

	scope := strings.TrimSpace(strings.ToLower(c.Query("scope")))
	enabledOnly := parseBoolLike(c.Query("enabledOnly"))

	tx := h.db.Model(&models.MetaPresetModel{})
	if scope != "" && scope != "both" {
		tx = tx.Where("scope = ? OR scope = ?", scope, "both")
	}
	if enabledOnly {
		tx = tx.Where("enabled = ?", true)
	}

	var items []models.MetaPresetModel
	if err := tx.Order("`order` ASC").Find(&items).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": items})
}

func (h *Handler) getByID(c *gin.Context) {
	h.ensureBuiltins()

	var item models.MetaPresetModel
	if err := h.db.First(&item, "id = ?", c.Param("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.NotFound(c)
			return
		}
		response.InternalError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *Handler) create(c *gin.Context) {
	h.ensureBuiltins()

	var dto createMetaPresetDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	dto.Key = strings.TrimSpace(dto.Key)
	dto.Label = strings.TrimSpace(dto.Label)
	dto.Type = strings.TrimSpace(dto.Type)
	if dto.Key == "" || dto.Label == "" || dto.Type == "" {
		response.BadRequest(c, "key, label, type are required")
		return
	}

	var count int64
	if err := h.db.Model(&models.MetaPresetModel{}).Where("`key` = ?", dto.Key).Count(&count).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	if count > 0 {
		response.Conflict(c, "preset key already exists")
		return
	}

	scope := strings.ToLower(strings.TrimSpace(dto.Scope))
	if scope == "" {
		scope = "both"
	}
	enabled := true
	if dto.Enabled != nil {
		enabled = *dto.Enabled
	}
	order := 0
	if dto.Order != nil {
		order = *dto.Order
	} else {
		var maxOrder struct {
			Max int `gorm:"column:max_order"`
		}
		_ = h.db.Model(&models.MetaPresetModel{}).
			Select("COALESCE(MAX(`order`), -1) + 1 AS max_order").
			Scan(&maxOrder).Error
		order = maxOrder.Max
	}

	item := models.MetaPresetModel{
		Key:               dto.Key,
		Label:             dto.Label,
		Type:              dto.Type,
		Description:       dto.Description,
		Placeholder:       dto.Placeholder,
		Scope:             scope,
		Options:           dto.Options,
		AllowCustomOption: dto.AllowCustomOption,
		Children:          dto.Children,
		IsBuiltin:         false,
		Order:             order,
		Enabled:           enabled,
	}
	if err := h.db.Create(&item).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, item)
}

func (h *Handler) update(c *gin.Context) {
	h.ensureBuiltins()

	var item models.MetaPresetModel
	if err := h.db.First(&item, "id = ?", c.Param("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.NotFound(c)
			return
		}
		response.InternalError(c, err)
		return
	}

	var dto updateMetaPresetDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	updates := map[string]interface{}{}
	if item.IsBuiltin {
		if dto.Enabled != nil {
			updates["enabled"] = *dto.Enabled
		}
		if dto.Order != nil {
			updates["order"] = *dto.Order
		}
	} else {
		if dto.Key != nil {
			key := strings.TrimSpace(*dto.Key)
			if key == "" {
				response.BadRequest(c, "key can not be empty")
				return
			}
			if key != item.Key {
				var count int64
				if err := h.db.Model(&models.MetaPresetModel{}).Where("`key` = ?", key).Count(&count).Error; err != nil {
					response.InternalError(c, err)
					return
				}
				if count > 0 {
					response.Conflict(c, "preset key already exists")
					return
				}
			}
			updates["key"] = key
		}
		if dto.Label != nil {
			updates["label"] = strings.TrimSpace(*dto.Label)
		}
		if dto.Type != nil {
			updates["type"] = strings.TrimSpace(*dto.Type)
		}
		if dto.Description != nil {
			updates["description"] = *dto.Description
		}
		if dto.Placeholder != nil {
			updates["placeholder"] = *dto.Placeholder
		}
		if dto.Scope != nil {
			scope := strings.ToLower(strings.TrimSpace(*dto.Scope))
			if scope == "" {
				scope = "both"
			}
			updates["scope"] = scope
		}
		if dto.Options != nil {
			updates["options"] = marshalJSONColumn(*dto.Options)
		}
		if dto.AllowCustomOption != nil {
			updates["allow_custom_option"] = *dto.AllowCustomOption
		}
		if dto.Children != nil {
			updates["children"] = marshalJSONColumn(*dto.Children)
		}
		if dto.Order != nil {
			updates["order"] = *dto.Order
		}
		if dto.Enabled != nil {
			updates["enabled"] = *dto.Enabled
		}
	}

	if len(updates) == 0 {
		response.OK(c, item)
		return
	}
	if err := h.db.Model(&item).Updates(updates).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	if err := h.db.First(&item, "id = ?", item.ID).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *Handler) delete(c *gin.Context) {
	h.ensureBuiltins()

	var item models.MetaPresetModel
	if err := h.db.First(&item, "id = ?", c.Param("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.NotFound(c)
			return
		}
		response.InternalError(c, err)
		return
	}
	if item.IsBuiltin {
		response.ForbiddenMsg(c, "内置预设字段不能删除")
		return
	}
	if err := h.db.Delete(&item).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) updateOrder(c *gin.Context) {
	h.ensureBuiltins()

	var dto updateOrderDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	for i, id := range dto.IDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		_ = h.db.Model(&models.MetaPresetModel{}).Where("id = ?", id).Update("`order`", i).Error
	}

	var items []models.MetaPresetModel
	if err := h.db.Order("`order` ASC").Find(&items).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": items})
}

func parseBoolLike(raw string) bool {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "1" {
		return true
	}
	v, err := strconv.ParseBool(raw)
	return err == nil && v
}

func marshalJSONColumn(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}
