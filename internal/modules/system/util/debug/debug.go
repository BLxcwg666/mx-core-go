package debug

import (
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/modules/gateway/gateway"
	"github.com/mx-space/core/internal/modules/serverless"
	"github.com/mx-space/core/internal/pkg/response"
)

type Handler struct {
	hub *gateway.Hub
	fn  *serverless.Handler
}

func NewHandler(hub *gateway.Hub, fn *serverless.Handler) *Handler {
	return &Handler{hub: hub, fn: fn}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/debug", authMW)
	g.GET("/test", h.test)
	g.POST("/events", h.sendEvent)
	g.POST("/function", h.runFunction)
}

func (h *Handler) test(c *gin.Context) {
	c.String(200, "")
}

func (h *Handler) sendEvent(c *gin.Context) {
	event := strings.TrimSpace(c.Query("event"))
	if event == "" {
		response.BadRequest(c, "event is required")
		return
	}

	broadcastType := strings.TrimSpace(strings.ToLower(c.DefaultQuery("type", "web")))
	var payload interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// The body is broadcast as the event data itself (plus `date` for objects), as the original core does;
	// the admin handlers read fields like `type`/`message` straight from it.
	data := payload
	if obj, ok := payload.(map[string]interface{}); ok {
		if _, exists := obj["date"]; !exists {
			obj["date"] = time.Now()
		}
		data = obj
	}
	if h.hub != nil {
		switch broadcastType {
		case "admin":
			h.hub.BroadcastAdmin(event, data)
		case "all":
			h.hub.BroadcastAdmin(event, data)
			h.hub.BroadcastPublic(event, data)
		default:
			h.hub.BroadcastPublic(event, data)
		}
	}
	response.NoContent(c)
}

func (h *Handler) runFunction(c *gin.Context) {
	var body struct {
		Function string `json:"function" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if h.fn == nil {
		response.InternalError(c, errServerlessUnavailable)
		return
	}
	h.fn.RunDebug(c, body.Function)
}

var errServerlessUnavailable = errors.New("serverless runtime is unavailable")
