package category

import (
	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/pkg/response"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	cats := rg.Group("/categories")
	cats.GET("", h.list)
	cats.GET("/:query", h.getByQuery)

	authed := cats.Group("", authMW)
	authed.POST("", h.create)
	authed.PUT("/:id", h.update)
	authed.PATCH("/:id", h.update)
	authed.DELETE("/:id", h.delete)
}

func (h *Handler) list(c *gin.Context) {
	cats, err := h.svc.List()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, gin.H{"data": cats})
}

func (h *Handler) getByQuery(c *gin.Context) {
	cat, err := h.svc.GetByQuery(c.Param("query"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if cat == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, cat)
}

func (h *Handler) create(c *gin.Context) {
	var dto CreateCategoryDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	cat, err := h.svc.Create(&dto)
	if err != nil {
		if err.Error() == "name or slug already exists" {
			response.Conflict(c, err.Error())
			return
		}
		response.InternalError(c, err)
		return
	}
	response.Created(c, cat)
}

func (h *Handler) update(c *gin.Context) {
	var dto UpdateCategoryDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	cat, err := h.svc.Update(c.Param("id"), &dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if cat == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, cat)
}

func (h *Handler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Param("id")); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}
