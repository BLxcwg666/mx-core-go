package dependency

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/modules/pty"
	"github.com/mx-space/core/internal/pkg/response"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/dependencies", authMW)
	g.GET("/graph", h.graph)
	g.GET("/install_deps", h.installDeps)
}

func (h *Handler) graph(c *gin.Context) {
	type pkgJSON struct {
		Dependencies map[string]string `json:"dependencies"`
	}

	var parsed pkgJSON
	for _, candidate := range []string{
		filepath.Join(".", "tmp", "package.json"),
		filepath.Join(".", "package.json"),
	} {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		if err := json.Unmarshal(data, &parsed); err == nil {
			break
		}
	}

	if parsed.Dependencies == nil {
		parsed.Dependencies = map[string]string{}
	}
	c.JSON(200, gin.H{"dependencies": parsed.Dependencies})
}

func (h *Handler) installDeps(c *gin.Context) {
	packageNames := strings.TrimSpace(c.Query("packageNames"))
	if packageNames == "" {
		response.BadRequest(c, "packageNames must be provided")
		return
	}

	sessionID := pty.StartSession(c.ClientIP())
	defer pty.EndSession(sessionID)

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(200)

	messages := []string{
		"mx-core-go does not include Node.js runtime dependency installer.",
		"requested packages: " + packageNames,
		"skip install, no changes applied.",
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
