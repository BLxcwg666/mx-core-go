package debug

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/modules/gateway"
	"github.com/mx-space/core/internal/pkg/response"
)

type Handler struct {
	hub *gateway.Hub
}

func NewHandler(hub *gateway.Hub) *Handler { return &Handler{hub: hub} }

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

	data := gin.H{
		"event":   event,
		"payload": payload,
		"date":    time.Now(),
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

	vm := goja.New()

	resState := &runtimeResponse{status: http.StatusOK}
	ctxObj := h.buildDebugContext(vm, c, resState)

	fn, err := resolveDebugFunction(vm, strings.TrimSpace(body.Function))
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	result, err := fn(goja.Undefined(), ctxObj)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if resState.sent {
		h.flushRuntimeResponse(c, resState)
		return
	}

	resolved, err := resolveDebugResult(result)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, resolved)
}

type runtimeResponse struct {
	status      int
	contentType string
	payload     interface{}
	sent        bool
}

func (h *Handler) buildDebugContext(vm *goja.Runtime, c *gin.Context, state *runtimeResponse) goja.Value {
	reqObj := vm.NewObject()
	_ = reqObj.Set("method", c.Request.Method)
	_ = reqObj.Set("path", c.Request.URL.Path)
	_ = reqObj.Set("query", c.Request.URL.Query())
	_ = reqObj.Set("headers", c.Request.Header)

	resObj := vm.NewObject()
	_ = resObj.Set("status", func(call goja.FunctionCall) goja.Value {
		if call.Argument(0) != nil && !goja.IsUndefined(call.Argument(0)) && !goja.IsNull(call.Argument(0)) {
			state.status = int(call.Argument(0).ToInteger())
		}
		return resObj
	})
	_ = resObj.Set("type", func(call goja.FunctionCall) goja.Value {
		if call.Argument(0) != nil && !goja.IsUndefined(call.Argument(0)) && !goja.IsNull(call.Argument(0)) {
			state.contentType = call.Argument(0).String()
		}
		return resObj
	})
	_ = resObj.Set("send", func(call goja.FunctionCall) goja.Value {
		state.payload = exportJSValue(call.Argument(0))
		state.sent = true
		return goja.Undefined()
	})
	_ = resObj.Set("json", func(call goja.FunctionCall) goja.Value {
		state.payload = exportJSValue(call.Argument(0))
		state.sent = true
		return goja.Undefined()
	})
	_ = resObj.Set("throws", func(call goja.FunctionCall) goja.Value {
		code := int64(500)
		if call.Argument(0) != nil && !goja.IsUndefined(call.Argument(0)) && !goja.IsNull(call.Argument(0)) {
			code = call.Argument(0).ToInteger()
		}
		message := "runtime error"
		if call.Argument(1) != nil && !goja.IsUndefined(call.Argument(1)) && !goja.IsNull(call.Argument(1)) {
			message = call.Argument(1).String()
		}
		panic(vm.NewGoError(fmt.Errorf("%d:%s", code, message)))
	})

	ctx := vm.NewObject()
	_ = ctx.Set("req", reqObj)
	_ = ctx.Set("res", resObj)
	_ = ctx.Set("isAuthenticated", true)

	_ = vm.Set("require", func(goja.FunctionCall) goja.Value {
		return vm.NewObject()
	})

	return ctx
}

func resolveDebugFunction(vm *goja.Runtime, src string) (goja.Callable, error) {
	if src == "" {
		return nil, fmt.Errorf("function is empty")
	}

	if fn := evalCallable(vm, "("+src+")"); fn != nil {
		return fn, nil
	}

	_ = vm.Set("exports", vm.NewObject())
	moduleObj := vm.NewObject()
	_ = moduleObj.Set("exports", vm.Get("exports"))
	_ = vm.Set("module", moduleObj)

	normalized := strings.Replace(src, "export default", "var __mx_default__ =", 1)
	if _, err := vm.RunString(normalized); err != nil {
		return nil, err
	}

	candidates := []goja.Value{
		vm.Get("__mx_default__"),
		vm.Get("handler"),
		vm.Get("module").ToObject(vm).Get("exports"),
		vm.Get("exports").ToObject(vm).Get("default"),
	}
	for _, candidate := range candidates {
		if fn, ok := goja.AssertFunction(candidate); ok {
			return fn, nil
		}
	}

	return nil, fmt.Errorf("no callable function found; expected export default or handler")
}

func evalCallable(vm *goja.Runtime, expr string) goja.Callable {
	v, err := vm.RunString(expr)
	if err != nil {
		return nil
	}
	if fn, ok := goja.AssertFunction(v); ok {
		return fn
	}
	return nil
}

func resolveDebugResult(v goja.Value) (interface{}, error) {
	if v == nil || goja.IsNull(v) || goja.IsUndefined(v) {
		return nil, nil
	}
	if p, ok := v.Export().(*goja.Promise); ok {
		switch p.State() {
		case goja.PromiseStateRejected:
			return nil, fmt.Errorf("promise rejected: %v", exportJSValue(p.Result()))
		case goja.PromiseStatePending:
			return nil, fmt.Errorf("promise is still pending; use synchronous return in debug mode")
		default:
			return exportJSValue(p.Result()), nil
		}
	}
	return exportJSValue(v), nil
}

func exportJSValue(v goja.Value) interface{} {
	if v == nil || goja.IsNull(v) || goja.IsUndefined(v) {
		return nil
	}
	return v.Export()
}

func (h *Handler) flushRuntimeResponse(c *gin.Context, state *runtimeResponse) {
	if state.status <= 0 {
		state.status = http.StatusOK
	}
	if state.contentType != "" {
		if s, ok := state.payload.(string); ok {
			c.Data(state.status, state.contentType, []byte(s))
			return
		}
		c.Header("Content-Type", state.contentType)
	}
	c.JSON(state.status, state.payload)
}
