package webhook

import (
	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
)

// Handler wires webhook HTTP endpoints.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/webhooks", authMW)
	g.GET("", h.list)
	g.POST("", h.create)
	g.PUT("/:id", h.update)
	g.PATCH("/:id", h.update)
	g.DELETE("/:id", h.delete)

	g.GET("/events", h.listEventsEnum)
	g.GET("/dispatches", h.listEvents)
	g.POST("/redispatch/:id", h.redispatch)
	g.DELETE("/clear/:id", h.clearEvents)
	g.GET("/:id", h.listEventsByHook)
}

func (h *Handler) list(c *gin.Context) {
	items, err := h.svc.List()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	out := make([]webhookResponse, len(items))
	for i, w := range items {
		out[i] = toResponse(&w)
	}
	response.OK(c, out)
}

func (h *Handler) create(c *gin.Context) {
	var dto CreateWebhookDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	w, err := h.svc.Create(&dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, toResponse(w))
}

func (h *Handler) update(c *gin.Context) {
	var dto UpdateWebhookDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	w, err := h.svc.Update(c.Param("id"), &dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if w == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, toResponse(w))
}

func (h *Handler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Param("id")); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) listEventsEnum(c *gin.Context) {
	response.OK(c, webhookEventEnum)
}

func (h *Handler) listEvents(c *gin.Context) {
	q := pagination.FromContext(c)
	hookID := c.Query("hookId")
	var hookIDPtr *string
	if hookID != "" {
		hookIDPtr = &hookID
	}
	items, pag, err := h.svc.ListEvents(q, hookIDPtr)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Paged(c, items, pag)
}

func (h *Handler) listEventsByHook(c *gin.Context) {
	q := pagination.FromContext(c)
	hookID := c.Param("id")
	items, pag, err := h.svc.ListEvents(q, &hookID)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Paged(c, items, pag)
}

func (h *Handler) redispatch(c *gin.Context) {
	if err := h.svc.Redispatch(c.Param("id")); err != nil {
		if err.Error() == "event not found" || err.Error() == "hook not found" {
			response.NotFoundMsg(c, err.Error())
			return
		}
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) clearEvents(c *gin.Context) {
	if err := h.svc.ClearEventsByHookID(c.Param("id")); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}
