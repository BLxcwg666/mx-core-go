package serverless

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/gateway/gateway"
	jwtpkg "github.com/mx-space/core/internal/pkg/jwt"
	pkgredis "github.com/mx-space/core/internal/pkg/redis"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type Handler struct {
	db         *gorm.DB
	hub        *gateway.Hub
	rc         *pkgredis.Client
	httpClient *http.Client

	compiledMu sync.RWMutex
	compiled   map[string]compiledSnippet

	builtInMu    sync.Mutex
	builtInReady bool
}

func NewHandler(db *gorm.DB, hub *gateway.Hub, rc *pkgredis.Client) *Handler {
	return &Handler{
		db:         db,
		hub:        hub,
		rc:         rc,
		httpClient: &http.Client{Timeout: 8 * time.Second},
		compiled:   map[string]compiledSnippet{},
	}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	for _, prefix := range []string{"/serverless", "/fn"} {
		g := rg.Group(prefix)
		g.GET("/types", authMW, h.getTypes)
		g.DELETE("/reset/:id", authMW, h.reset)
		g.Any("/:reference/:name/*path", h.run)
		g.Any("/:reference/:name", h.run)
	}
}

func (h *Handler) getTypes(c *gin.Context) {
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.String(http.StatusOK, defaultTypeDefinition)
}

const defaultTypeDefinition = `type Context = {
  req: {
    method: string
    path: string
    query: Record<string, string | string[]>
    body: any
    headers: Record<string, string>
  }
  res: {
    status: (code: number) => void
    json: (data: any) => void
    send: (data: any) => void
  }
  isAuthenticated: boolean
}
`

func (h *Handler) reset(c *gin.Context) {
	if err := h.ensureBuiltInSnippets(); err != nil {
		response.InternalError(c, err)
		return
	}

	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.NotFoundMsg(c, "函数不存在")
		return
	}

	var snippet models.SnippetModel
	err := h.db.First(&snippet, "id = ?", id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			response.NotFoundMsg(c, "函数不存在")
			return
		}
		response.InternalError(c, err)
		return
	}

	if strings.EqualFold(string(snippet.Type), string(snippetTypeFunction)) && snippet.BuiltIn {
		if err := h.resetBuiltInSnippet(&snippet); err != nil {
			response.InternalError(c, err)
			return
		}
		response.NoContent(c)
		return
	}

	if err := h.db.Delete(&models.SnippetModel{}, "id = ?", id).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) run(c *gin.Context) {
	if err := h.ensureBuiltInSnippets(); err != nil {
		response.InternalError(c, err)
		return
	}

	reference := strings.TrimSpace(c.Param("reference"))
	name := strings.TrimSpace(c.Param("name"))
	if reference == "" || name == "" {
		response.NotFoundMsg(c, "函数不存在")
		return
	}

	reqMethod := strings.ToUpper(c.Request.Method)
	var snippet models.SnippetModel
	err := h.db.
		Where("reference = ? AND name = ?", reference, name).
		Where("LOWER(type) = ?", string(snippetTypeFunction)).
		Where("(UPPER(method) = ? OR UPPER(method) = 'ALL' OR method = '' OR method IS NULL)", reqMethod).
		First(&snippet).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			response.NotFoundMsg(c, fmt.Sprintf("函数不存在: %s/%s", reference, name))
			return
		}
		response.InternalError(c, err)
		return
	}

	if !snippet.Enable {
		response.BadRequest(c, "函数已被禁用")
		return
	}
	if snippet.Private && !hasFunctionAccess(c) {
		response.ForbiddenMsg(c, "没有权限运行该函数")
		return
	}

	runtimeCtx := h.buildRuntimeContext(c, &snippet)
	out, runErr := h.executeSnippet(&snippet, runtimeCtx)
	if runErr != nil {
		var execErr *runtimeExecError
		if ok := asRuntimeExecError(runErr, &execErr); ok {
			c.AbortWithStatusJSON(execErr.Status, gin.H{
				"message":     execErr.Message,
				"status_code": execErr.Status,
			})
			return
		}
		response.InternalError(c, runErr)
		return
	}

	h.writeServerlessResponse(c, out)
}

func hasFunctionAccess(c *gin.Context) bool {
	if middleware.IsAuthenticated(c) {
		return true
	}
	token := strings.TrimSpace(c.Query("token"))
	if token == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	_, err := jwtpkg.Parse(token)
	return err == nil
}
