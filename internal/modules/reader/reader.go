package reader

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type Handler struct {
	db *gorm.DB
}

func NewHandler(db *gorm.DB) *Handler { return &Handler{db: db} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/readers", authMW)
	g.GET("", h.list)
	g.PATCH("/as-owner", h.asOwner)
	g.PATCH("/revoke-owner", h.revokeOwner)
}

type readerResponse struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Image    string `json:"image"`
	Handle   string `json:"handle"`
	IsOwner  bool   `json:"isOwner"`
}

func (h *Handler) list(c *gin.Context) {
	q := pagination.FromContext(c)
	tx := h.db.Model(&models.ReaderModel{}).Order("created_at DESC")

	var rows []models.ReaderModel
	pag, err := pagination.Paginate(tx, q, &rows)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	out := make([]readerResponse, 0, len(rows))
	for _, row := range rows {
		provider, accountType := inferProviderAndType(row.Handle)
		out = append(out, readerResponse{
			ID:       row.ID,
			Provider: provider,
			Type:     accountType,
			Name:     row.Name,
			Email:    row.Email,
			Image:    row.Image,
			Handle:   row.Handle,
			IsOwner:  row.IsOwner,
		})
	}

	c.JSON(200, gin.H{
		"data":       out,
		"pagination": pag,
	})
}

type ownerPatchDTO struct {
	ID string `json:"id" binding:"required"`
}

func (h *Handler) asOwner(c *gin.Context) {
	var dto ownerPatchDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.db.Model(&models.ReaderModel{}).
		Where("id = ?", dto.ID).
		Update("is_owner", true).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) revokeOwner(c *gin.Context) {
	var dto ownerPatchDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.db.Model(&models.ReaderModel{}).
		Where("id = ?", dto.ID).
		Update("is_owner", false).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func inferProviderAndType(handle string) (provider, accountType string) {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return "credentials", "credentials"
	}

	parts := strings.Split(handle, ":")
	if len(parts) >= 2 && parts[0] != "" {
		return strings.ToLower(parts[0]), "oauth"
	}

	switch {
	case strings.Contains(strings.ToLower(handle), "github"):
		return "github", "oauth"
	default:
		return "credentials", "credentials"
	}
}
