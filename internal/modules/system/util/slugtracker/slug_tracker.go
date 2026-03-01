package slugtracker

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

// Service provides slug tracking operations.
type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

// Track records that oldSlug for the given content type now points to targetID.
func (s *Service) Track(oldSlug, refType, targetID string) error {
	tracker := models.SlugTrackerModel{
		Slug:     oldSlug,
		Type:     refType,
		TargetID: targetID,
	}

	return s.db.Where(models.SlugTrackerModel{Slug: oldSlug, Type: refType}).
		Assign(models.SlugTrackerModel{TargetID: targetID}).
		FirstOrCreate(&tracker).Error
}

// FindBySlug returns the current targetID for the given old slug, or ("", nil)
func (s *Service) FindBySlug(slug, refType string) (string, error) {
	var tracker models.SlugTrackerModel
	err := s.db.Where("slug = ? AND type = ?", slug, refType).First(&tracker).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return tracker.TargetID, nil
}

// DeleteByTargetID removes all tracker entries for a given content item.
func (s *Service) DeleteByTargetID(targetID string) error {
	return s.db.Where("target_id = ?", targetID).Delete(&models.SlugTrackerModel{}).Error
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/slug-tracker")
	g.GET("/redirect/:type/:slug", h.redirect)
	g.GET("/:type/:slug", authMW, h.lookup)
	g.DELETE("/:type/:slug", authMW, h.remove)
}

// GET /slug-tracker/redirect/:type/:slug — public redirect lookup
func (h *Handler) redirect(c *gin.Context) {
	refType := c.Param("type")
	slug := c.Param("slug")

	targetID, err := h.svc.FindBySlug(slug, refType)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if targetID == "" {
		response.NotFoundMsg(c, "内容不存在")
		return
	}
	response.OK(c, gin.H{"target_id": targetID, "type": refType, "slug": slug})
}

func (h *Handler) lookup(c *gin.Context) {
	refType := c.Param("type")
	slug := c.Param("slug")

	targetID, err := h.svc.FindBySlug(slug, refType)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if targetID == "" {
		response.NotFoundMsg(c, "内容不存在")
		return
	}
	response.OK(c, gin.H{"target_id": targetID, "type": refType, "slug": slug})
}

func (h *Handler) remove(c *gin.Context) {
	refType := c.Param("type")
	slug := c.Param("slug")

	if err := h.svc.db.Where("slug = ? AND type = ?", slug, refType).
		Delete(&models.SlugTrackerModel{}).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}
