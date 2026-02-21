package ack

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/gateway"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type Handler struct {
	db  *gorm.DB
	hub *gateway.Hub
}

func NewHandler(db *gorm.DB, hub *gateway.Hub) *Handler {
	return &Handler{db: db, hub: hub}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	g := rg.Group("/ack")
	g.POST("", h.ack)
}

type ackDTO struct {
	Type    string                 `json:"type" binding:"required"`
	Payload map[string]interface{} `json:"payload" binding:"required"`
}

func (h *Handler) ack(c *gin.Context) {
	var dto ackDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if strings.ToLower(strings.TrimSpace(dto.Type)) != "read" {
		response.BadRequest(c, "only read ack is supported")
		return
	}

	refType := strings.ToLower(strings.TrimSpace(getStr(dto.Payload, "type")))
	refID := strings.TrimSpace(getStr(dto.Payload, "id"))
	if refType == "" || refID == "" {
		response.BadRequest(c, "payload.type and payload.id are required")
		return
	}

	switch refType {
	case "post":
		if err := h.db.Model(&models.PostModel{}).
			Where("id = ?", refID).
			UpdateColumn("read_count", gorm.Expr("read_count + 1")).Error; err != nil {
			response.InternalError(c, err)
			return
		}
		var post models.PostModel
		if err := h.db.Select("id, read_count").First(&post, "id = ?", refID).Error; err == nil && h.hub != nil {
			h.hub.BroadcastPublic("ARTICLE_READ_COUNT_UPDATE", gin.H{
				"id":    refID,
				"type":  "post",
				"count": post.ReadCount,
			})
		}
	case "note":
		if err := h.db.Model(&models.NoteModel{}).
			Where("id = ?", refID).
			UpdateColumn("read_count", gorm.Expr("read_count + 1")).Error; err != nil {
			response.InternalError(c, err)
			return
		}
		var note models.NoteModel
		if err := h.db.Select("id, read_count").First(&note, "id = ?", refID).Error; err == nil && h.hub != nil {
			h.hub.BroadcastPublic("ARTICLE_READ_COUNT_UPDATE", gin.H{
				"id":    refID,
				"type":  "note",
				"count": note.ReadCount,
			})
		}
	case "page":
		if err := h.db.Model(&models.PageModel{}).
			Where("id = ?", refID).
			UpdateColumn("read_count", gorm.Expr("read_count + 1")).Error; err != nil {
			response.InternalError(c, err)
			return
		}
		var pg models.PageModel
		if err := h.db.Select("id, read_count").First(&pg, "id = ?", refID).Error; err == nil && h.hub != nil {
			h.hub.BroadcastPublic("ARTICLE_READ_COUNT_UPDATE", gin.H{
				"id":    refID,
				"type":  "page",
				"count": pg.ReadCount,
			})
		}
	default:
		response.BadRequest(c, "payload.type must be post|note|page")
		return
	}

	c.Status(200)
}

func getStr(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
