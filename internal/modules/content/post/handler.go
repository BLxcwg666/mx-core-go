package post

import (
	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/gateway/notify"
	"github.com/mx-space/core/internal/modules/processing/textmacro"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
)

// Handler handles post HTTP requests.
type Handler struct {
	svc       *Service
	notifySvc *notify.Service
	macroSvc  *textmacro.Service
}

func NewHandler(svc *Service, notifySvc *notify.Service, macroSvc *textmacro.Service) *Handler {
	return &Handler{svc: svc, notifySvc: notifySvc, macroSvc: macroSvc}
}

// RegisterRoutes mounts post routes onto the given router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	posts := rg.Group("/posts")

	posts.GET("", h.list)
	posts.GET("/latest", h.latest)
	posts.GET("/get-url/:slug", h.getURLBySlug)
	posts.GET("/:identifier/:slug", h.getByCategoryAndSlug)
	posts.GET("/:identifier", h.getByIdentifier)
	posts.POST("/:id/like", h.like)

	authed := posts.Group("", authMW)
	authed.POST("", h.create)
	authed.PUT("/:id", h.update)
	authed.PATCH("/:id", h.update)          // legacy compatibility
	authed.PATCH("/:id/publish", h.publish) // returns {success:true} for TS compatibility
	authed.DELETE("/:id", h.delete)
}

// list GET /posts
func (h *Handler) list(c *gin.Context) {
	q := pagination.FromContext(c)

	var lq ListQuery
	if err := c.ShouldBindQuery(&lq); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	posts, pag, err := h.svc.List(q, lq)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	items := make([]postResponse, len(posts))
	for i, p := range posts {
		items[i] = toResponse(&p)
	}
	response.Paged(c, items, pag)
}

// getByIdentifier GET /posts/:identifier
func (h *Handler) getByIdentifier(c *gin.Context) {
	identifier := c.Param("identifier")
	isAdmin := middleware.IsAuthenticated(c)

	post, err := h.svc.GetByIdentifier(identifier, isAdmin)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if post == nil {
		response.NotFoundMsg(c, "文章不存在")
		return
	}

	go func() { _ = h.svc.IncrementReadCount(post.ID) }()

	resp := toResponse(post)
	h.applyMacros(&resp, isAdmin)
	response.OK(c, resp)
}

// getURLBySlug GET /posts/get-url/:slug
func (h *Handler) getURLBySlug(c *gin.Context) {
	post, err := h.svc.GetBySlug(c.Param("slug"), true)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if post == nil {
		response.NotFoundMsg(c, "文章不存在")
		return
	}
	categorySlug := post.Category.Slug
	if categorySlug == "" {
		categorySlug = "uncategorized"
	}
	response.OK(c, gin.H{
		"path": "/" + categorySlug + "/" + post.Slug,
	})
}

// getByCategoryAndSlug GET /posts/:category/:slug
func (h *Handler) getByCategoryAndSlug(c *gin.Context) {
	category := c.Param("category")
	if category == "" {
		category = c.Param("identifier")
	}
	slug := c.Param("slug")
	isAdmin := middleware.IsAuthenticated(c)

	post, err := h.svc.GetByCategoryAndSlug(category, slug, isAdmin)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if post == nil {
		response.NotFoundMsg(c, "文章不存在")
		return
	}

	go func() { _ = h.svc.IncrementReadCount(post.ID) }()

	resp := toResponse(post)
	h.applyMacros(&resp, isAdmin)
	response.OK(c, resp)
}

// latest GET /posts/latest
func (h *Handler) latest(c *gin.Context) {
	post, err := h.svc.GetLatest(middleware.IsAuthenticated(c))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if post == nil {
		response.NotFoundMsg(c, "文章不存在")
		return
	}
	response.OK(c, toResponse(post))
}

// like POST /posts/:id/like
func (h *Handler) like(c *gin.Context) {
	id := c.Param("id")
	if err := h.svc.IncrementLikeCount(id); err != nil {
		response.InternalError(c, err)
		return
	}
	_ = h.svc.db.Create(&models.ActivityModel{
		Type: "0",
		Payload: map[string]interface{}{
			"id":   id,
			"type": "post",
			"ip":   c.ClientIP(),
		},
	}).Error
	response.NoContent(c)
}

// create POST /posts  [auth]
func (h *Handler) create(c *gin.Context) {
	var dto CreatePostDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	post, err := h.svc.Create(&dto)
	if err != nil {
		if err.Error() == "slug already exists" {
			response.Conflict(c, err.Error())
			return
		}
		if err.Error() == "category is required" || err.Error() == "category not found" {
			response.BadRequest(c, err.Error())
			return
		}
		response.InternalError(c, err)
		return
	}

	if h.notifySvc != nil && post.IsPublished {
		go h.notifySvc.OnPostCreate(post)
	}

	response.Created(c, toResponse(post))
}

// update PUT /posts/:id  [auth]
func (h *Handler) update(c *gin.Context) {
	id := c.Param("id")

	var dto UpdatePostDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	post, err := h.svc.Update(id, &dto)
	if err != nil {
		if err.Error() == "category is required" || err.Error() == "category not found" {
			response.BadRequest(c, err.Error())
			return
		}
		response.InternalError(c, err)
		return
	}
	if post == nil {
		response.NotFoundMsg(c, "文章不存在")
		return
	}

	response.OK(c, toResponse(post))
}

// publish PATCH /posts/:id/publish  [auth]
// Returns {success:true} for TypeScript API compatibility.
func (h *Handler) publish(c *gin.Context) {
	id := c.Param("id")

	var dto UpdatePostDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	_, err := h.svc.Update(id, &dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	response.OK(c, gin.H{"success": true})
}

// delete DELETE /posts/:id  [auth]
func (h *Handler) delete(c *gin.Context) {
	id := c.Param("id")
	if err := h.svc.Delete(id); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

// applyMacros processes text macros in the post response if the macro service is available.
func (h *Handler) applyMacros(resp *postResponse, isAuthenticated bool) {
	if h.macroSvc == nil {
		return
	}
	fields := textmacro.Fields{
		"title":            resp.Title,
		"slug":             resp.Slug,
		"summary":          resp.Summary,
		"id":               resp.ID,
		"created":          resp.Created,
		"modified":         resp.Modified,
		"isPublished":      resp.IsPublished,
		"_isAuthenticated": isAuthenticated,
	}
	resp.Text = h.macroSvc.Process(resp.Text, fields)
}
