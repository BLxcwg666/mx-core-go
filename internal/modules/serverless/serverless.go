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

// defaultTypeDefinition describes what the runtime (installRuntimeGlobals) exposes, for the admin editor.
const defaultTypeDefinition = `interface ServerlessResponse {
  status(code: number): ServerlessResponse
  type(contentType: string): ServerlessResponse
  json(data: any): void
  send(data: any): void
  throws(code: number, message: any): never
}

interface ServerlessRequest {
  method: string
  path: string
  url: string
  ip: string
  query: Record<string, any>
  params: Record<string, string>
  headers: Record<string, string>
  body: any
}

interface ServerlessCache {
  get(key: string): Promise<any>
  set(key: string, value: any, ttlSeconds?: number): Promise<void>
  del(key: string): Promise<void>
}

interface ServerlessDB {
  get(key: string): Promise<any>
  find(condition: Record<string, any>): Promise<any[]>
  set(key: string, value: any): Promise<void>
  insert(key: string, value: any): Promise<void>
  update(key: string, value: any): Promise<void>
  del(key: string): Promise<void>
}

interface AxiosLike {
  get<T = any>(url: string, config?: any): Promise<{ data: T; status: number; headers: Record<string, string> }>
  delete<T = any>(url: string, config?: any): Promise<{ data: T; status: number; headers: Record<string, string> }>
  post<T = any>(url: string, data?: any, config?: any): Promise<{ data: T; status: number; headers: Record<string, string> }>
  put<T = any>(url: string, data?: any, config?: any): Promise<{ data: T; status: number; headers: Record<string, string> }>
  patch<T = any>(url: string, data?: any, config?: any): Promise<{ data: T; status: number; headers: Record<string, string> }>
  request<T = any>(config: any): Promise<{ data: T; status: number; headers: Record<string, string> }>
}

interface Context {
  req: ServerlessRequest
  res: ServerlessResponse
  query: Record<string, any>
  params: Record<string, string>
  headers: Record<string, string>
  method: string
  path: string
  url: string
  ip: string
  body: any
  isAuthenticated: boolean
  secret: Record<string, any>
  model: { id: string; name: string; reference: string }
  document: { id: string; name: string; reference: string }
  name: string
  reference: string
  storage: { cache: ServerlessCache; db: ServerlessDB }
  getService(name: 'http'): Promise<{ axios: AxiosLike }>
  getService(name: 'config'): Promise<{ get(key: string): Promise<any> }>
  getMaster(): Promise<any>
  broadcast(type: string, payload: any): void
  writeAsset(path: string, data: any, options?: any): Promise<void>
  readAsset(path: string, options?: any): Promise<any>
  throws(code: number, message: any): never
  status(code: number): ServerlessResponse
}

declare const context: Context
declare const secret: Record<string, any>
declare const logger: Console
declare function require(id: 'url' | 'node:url'): { URL: typeof URL; URLSearchParams: typeof URLSearchParams }
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
	if snippet.Private && !h.hasFunctionAccess(c) {
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

func (h *Handler) hasFunctionAccess(c *gin.Context) bool {
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
	_, err := middleware.ValidateTokenClaims(h.db, token)
	return err == nil
}

const debugSnippetID = "__debug__"

// RunDebug runs code from the admin debug page with the same runtime as /fn (TypeScript, storage, services).
func (h *Handler) RunDebug(c *gin.Context, source string) {
	// A fixed ID with a fresh UpdatedAt keeps exactly one debug entry in the compile cache.
	snippet := &models.SnippetModel{
		Base:      models.Base{ID: debugSnippetID, UpdatedAt: time.Now()},
		Type:      snippetTypeFunction,
		Name:      "debug",
		Reference: "debug",
		Raw:       source,
		Enable:    true,
	}
	out, runErr := h.executeSnippet(snippet, h.buildRuntimeContext(c, snippet))
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

// EnsureBuiltIns seeds the built-in functions so they show up in the snippet list before the
// first /fn request.
func (h *Handler) EnsureBuiltIns() error {
	return h.ensureBuiltInSnippets()
}
