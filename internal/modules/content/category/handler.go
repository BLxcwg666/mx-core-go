package category

import (
	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/pkg/response"
)

type Handler struct {
	svc *Service
}

type listQuery struct {
	Type *int `form:"type"`
}

type getByQueryOptions struct {
	Tag *bool `form:"tag"`
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
	var query listQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	listType := CategoryTypeCategory
	if query.Type != nil {
		listType = *query.Type
	}

	switch listType {
	case CategoryTypeTag:
		tags, err := h.svc.ListTags()
		if err != nil {
			response.InternalError(c, err)
			return
		}
		response.OK(c, tags)
	default:
		cats, err := h.svc.ListCategories()
		if err != nil {
			response.InternalError(c, err)
			return
		}
		response.OK(c, cats)
	}
}

func (h *Handler) getByQuery(c *gin.Context) {
	var options getByQueryOptions
	if err := c.ShouldBindQuery(&options); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	query := c.Param("query")

	if options.Tag != nil && *options.Tag {
		posts, err := h.svc.ListPostsByTag(query)
		if err != nil {
			response.InternalError(c, err)
			return
		}
		if len(posts) == 0 {
			response.NotFoundMsg(c, "标签不存在")
			return
		}
		response.OK(c, gin.H{"tag": query, "data": posts})
		return
	}

	detail, err := h.svc.GetDetailByQuery(query)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if detail == nil {
		response.NotFoundMsg(c, "分类不存在")
		return
	}
	response.OK(c, gin.H{"data": detail})
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
		response.NotFoundMsg(c, "分类不存在")
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
