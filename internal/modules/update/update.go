package update

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/update", authMW)
	g.GET("/upgrade/dashboard", h.upgradeDashboard)
}

func (h *Handler) upgradeDashboard(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(200)

	messages := []string{
		"mx-core-go does not bundle dashboard auto-upgrade pipeline.",
		"Please upgrade admin assets manually in your deployment workflow.",
		"task finished.",
	}
	for _, msg := range messages {
		writeSSE(c, msg)
		time.Sleep(120 * time.Millisecond)
	}
}

func writeSSE(c *gin.Context, data string) {
	lines := strings.Split(data, "\n")
	for _, line := range lines {
		_, _ = c.Writer.WriteString("data: " + line + "\n")
	}
	_, _ = c.Writer.WriteString("\n")
	c.Writer.Flush()
}
