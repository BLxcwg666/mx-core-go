package note

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	notes := rg.Group("/notes")

	notes.GET("", h.list)
	notes.GET("/latest", h.latest)
	notes.GET("/list/:id", h.listAround)
	notes.GET("/topics/:id", h.listByTopic)
	notes.GET("/nid/:nid", h.getByNID)
	notes.GET("/:id", h.getByID)
	notes.POST("/:id/like", h.like)

	authed := notes.Group("", authMW)
	authed.POST("", h.create)
	authed.PUT("/:id", h.update)
	authed.PATCH("/:id", h.update)         // legacy compatibility
	authed.PATCH("/:id/publish", h.update) // legacy compatibility
	authed.DELETE("/:id", h.delete)
}

func (h *Handler) list(c *gin.Context) {
	q := pagination.FromContext(c)
	notes, pag, err := h.svc.List(q)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	items := make([]noteResponse, len(notes))
	for i, n := range notes {
		items[i] = toResponse(&n)
	}
	response.Paged(c, items, pag)
}

func (h *Handler) getByNID(c *gin.Context) {
	nid, err := strconv.Atoi(c.Param("nid"))
	if err != nil {
		response.BadRequest(c, "invalid nid")
		return
	}
	isAdmin := middleware.IsAuthenticated(c)
	note, err := h.svc.GetByNID(nid, isAdmin)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if note == nil {
		response.NotFoundMsg(c, "日记不存在")
		return
	}
	go func() { _ = h.svc.IncrementReadCount(note.ID) }()
	response.OK(c, toResponse(note))
}

func (h *Handler) getByID(c *gin.Context) {
	note, err := h.svc.GetByID(c.Param("id"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if note == nil {
		response.NotFoundMsg(c, "日记不存在")
		return
	}
	if !middleware.IsAuthenticated(c) && !note.IsPublished {
		response.ForbiddenMsg(c, "不要偷看人家的小心思啦~")
		return
	}
	go func() { _ = h.svc.IncrementReadCount(note.ID) }()
	response.OK(c, toResponse(note))
}

func (h *Handler) latest(c *gin.Context) {
	note, err := h.svc.GetLatest(middleware.IsAuthenticated(c))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if note == nil {
		response.NotFoundMsg(c, "日记不存在")
		return
	}
	response.OK(c, toResponse(note))
}

func (h *Handler) listByTopic(c *gin.Context) {
	q := pagination.FromContext(c)
	items, pag, err := h.svc.ListByTopic(c.Param("id"), q, middleware.IsAuthenticated(c))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	out := make([]noteResponse, len(items))
	for i, n := range items {
		out[i] = toResponse(&n)
	}
	response.Paged(c, out, pag)
}

func (h *Handler) listAround(c *gin.Context) {
	size := 10
	if raw := c.Query("size"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			size = parsed
		}
	}

	items, err := h.svc.ListAround(c.Param("id"), size, middleware.IsAuthenticated(c))
	if err != nil {
		response.InternalError(c, err)
		return
	}

	type timelineItem struct {
		ID          string `json:"id"`
		NID         int    `json:"nid"`
		Title       string `json:"title"`
		IsPublished bool   `json:"isPublished"`
		Created     any    `json:"created"`
		Modified    any    `json:"modified"`
	}

	out := make([]timelineItem, 0, len(items))
	for _, n := range items {
		var modified any
		if !n.UpdatedAt.IsZero() && n.UpdatedAt.Year() > 1 {
			modified = n.UpdatedAt
		}
		out = append(out, timelineItem{
			ID:          n.ID,
			NID:         n.NID,
			Title:       n.Title,
			IsPublished: n.IsPublished,
			Created:     n.CreatedAt,
			Modified:    modified,
		})
	}

	response.OK(c, gin.H{
		"data": out,
		"size": len(out),
	})
}

func (h *Handler) like(c *gin.Context) {
	if err := h.svc.IncrementLikeCount(c.Param("id")); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) create(c *gin.Context) {
	var dto CreateNoteDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	note, err := h.svc.Create(&dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, toResponse(note))
}

func (h *Handler) update(c *gin.Context) {
	var dto UpdateNoteDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	note, err := h.svc.Update(c.Param("id"), &dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if note == nil {
		response.NotFoundMsg(c, "日记不存在")
		return
	}
	response.OK(c, toResponse(note))
}

func (h *Handler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Param("id")); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}
