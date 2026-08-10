package option

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/gateway/webhook"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Handler struct {
	db      *gorm.DB
	cfgSvc  *appconfigs.Service
	webhook *webhook.Service
}

func NewHandler(db *gorm.DB, cfgSvc *appconfigs.Service, webhookSvc *webhook.Service) *Handler {
	return &Handler{db: db, cfgSvc: cfgSvc, webhook: webhookSvc}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	for _, prefix := range []string{"/option", "/kv/options"} {
		g := rg.Group(prefix, authMW)
		g.GET("", h.list)
		g.GET("/:key", h.get)
		g.PATCH("/:key", h.patch)
		g.DELETE("/:key", h.delete)
	}
}

func (h *Handler) list(c *gin.Context) {
	var items []models.OptionModel
	if err := h.db.Find(&items).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *Handler) get(c *gin.Context) {
	key := c.Param("key")
	var opt models.OptionModel
	if err := h.db.Where("name = ?", key).First(&opt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.NotFoundMsg(c, "设置不存在")
			return
		}
		response.InternalError(c, err)
		return
	}
	response.OK(c, opt)
}

type patchDTO struct {
	Value string `json:"value" binding:"required"`
}

func (h *Handler) patch(c *gin.Context) {
	key := c.Param("key")
	var dto patchDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	opt := models.OptionModel{Name: key, Value: dto.Value}
	if err := h.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&opt).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	h.invalidateConfig(key)
	response.OK(c, opt)
}

func (h *Handler) delete(c *gin.Context) {
	key := c.Param("key")
	if err := h.db.Where("name = ?", key).Delete(&models.OptionModel{}).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	h.invalidateConfig(key)
	response.NoContent(c)
}

func (h *Handler) invalidateConfig(key string) {
	if key != "configs" {
		return
	}
	if h.cfgSvc != nil {
		h.cfgSvc.Invalidate()
	}
	if h.webhook != nil {
		h.webhook.DispatchContentRefresh("config")
	}
}
