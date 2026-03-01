package option

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Handler struct{ db *gorm.DB }

func NewHandler(db *gorm.DB) *Handler { return &Handler{db: db} }

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
	response.OK(c, opt)
}

func (h *Handler) delete(c *gin.Context) {
	key := c.Param("key")
	if err := h.db.Where("name = ?", key).Delete(&models.OptionModel{}).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}
