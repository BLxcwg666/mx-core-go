package metapreset

import (
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
				Label:             "AI Participation",
				Type:              "checkbox",
				Scope:             "both",
				Description:       "Declare how AI participated in the writing process.",
				AllowCustomOption: true,
				Options: []models.MetaFieldOption{
					{Value: -1, Label: "No AI", Exclusive: true},
					{Value: 0, Label: "Assisted writing"},
					{Value: 1, Label: "Polishing"},
					{Value: 2, Label: "Fully generated", Exclusive: true},
				},
				IsBuiltin: true,
				Order:     0,
				Enabled:   true,
			},
			{
				Key:         "cover",
				Label:       "Cover",
				Type:        "url",
				Scope:       "both",
				Placeholder: "https://...",
				IsBuiltin:   true,
				Order:       1,
				Enabled:     true,
			},
			{
				Key:         "banner",
				Label:       "Banner",
				Type:        "object",
				Scope:       "both",
				Description: "Top banner for article/note detail page.",
				Children: []models.MetaPresetChild{
					{
						Key:   "type",
						Label: "Type",
						Type:  "select",
						Options: []models.MetaFieldOption{
							{Value: "info", Label: "Info"},
							{Value: "warning", Label: "Warning"},
							{Value: "error", Label: "Error"},
							{Value: "success", Label: "Success"},
							{Value: "secondary", Label: "Secondary"},
						},
					},
					{
						Key:   "message",
						Label: "Message",
						Type:  "textarea",
					},
					{
						Key:         "className",
						Label:       "Class Name",
						Type:        "text",
						Placeholder: "optional css class",
					},
				},
				IsBuiltin: true,
				Order:     2,
				Enabled:   true,
			},
			{
				Key:         "keywords",
				Label:       "SEO Keywords",
				Type:        "tags",
				Scope:       "both",
				Placeholder: "input keyword then press enter",
				IsBuiltin:   true,
				Order:       3,
				Enabled:     true,
			},
			{
				Key:         "style",
				Label:       "Style",
				Type:        "text",
				Scope:       "both",
				Placeholder: "article style name",
				IsBuiltin:   true,
				Order:       4,
				Enabled:     true,
			},
		}

		for _, preset := range builtins {
			var existing models.MetaPresetModel
			err := h.db.Where("`key` = ? AND is_builtin = ?", preset.Key, true).First(&existing).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					_ = h.db.Create(&preset).Error
				}
				continue
			}
			_ = h.db.Model(&existing).Updates(map[string]interface{}{
				"label":       preset.Label,
				"description": preset.Description,
				"placeholder": preset.Placeholder,
				"options":     preset.Options,
				"children":    preset.Children,
				"scope":       preset.Scope,
				"type":        preset.Type,
				"order":       preset.Order,
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
			updates["options"] = *dto.Options
		}
		if dto.AllowCustomOption != nil {
			updates["allow_custom_option"] = *dto.AllowCustomOption
		}
		if dto.Children != nil {
			updates["children"] = *dto.Children
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
		response.BadRequest(c, "builtin preset can not be deleted")
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
