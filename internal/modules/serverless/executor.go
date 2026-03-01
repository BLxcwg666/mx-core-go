package serverless

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
)

func (h *Handler) executeSnippet(snippet *models.SnippetModel, ctx runtimeContext) (*executorResult, error) {
	compiledCode, err := h.compileSnippet(snippet)
	if err != nil {
		return nil, &runtimeExecError{
			Status:  http.StatusInternalServerError,
			Message: err.Error(),
		}
	}

	vm := goja.New()
	meta := runtimeResponseMeta{StatusCode: http.StatusOK}
	timeoutReason := "serverless-timeout"
	timer := time.AfterFunc(serverlessExecutionTimeout, func() {
		vm.Interrupt(timeoutReason)
	})
	defer timer.Stop()

	if err := h.installRuntimeGlobals(vm, snippet, ctx, &meta); err != nil {
		return nil, err
	}

	bootstrap := "var module={exports:{}}; var exports=module.exports;\n" +
		compiledCode +
		"\n" +
		"globalThis.__mx_handler=(module.exports&&(module.exports.default||module.exports))||(exports&&exports.default)||(typeof handler==='function'?handler:null);" +
		"if(typeof globalThis.__mx_handler!=='function'){throw new Error('handler function is not defined')}"

	if _, err := vm.RunString(bootstrap); err != nil {
		return nil, h.normalizeRuntimeError(err, timeoutReason)
	}

	handlerVal := vm.Get("__mx_handler")
	handlerFn, ok := goja.AssertFunction(handlerVal)
	if !ok {
		return nil, &runtimeExecError{
			Status:  http.StatusInternalServerError,
			Message: "handler function is not callable",
		}
	}

	resultValue, err := handlerFn(goja.Undefined(), vm.Get("__mx_context"), vm.Get("require"))
	if err != nil {
		return nil, h.normalizeRuntimeError(err, timeoutReason)
	}

	result, hasData, err := h.resolveResultValue(resultValue)
	if err != nil {
		return nil, err
	}
	if meta.Sent {
		result = meta.SentData
		hasData = meta.SentHasData
	}

	return &executorResult{
		data:    result,
		hasData: hasData,
		meta:    meta,
	}, nil
}

func (h *Handler) installRuntimeGlobals(
	vm *goja.Runtime,
	snippet *models.SnippetModel,
	ctx runtimeContext,
	meta *runtimeResponseMeta,
) error {
	namespace := strings.TrimSpace(snippet.Reference) + "/" + strings.TrimSpace(snippet.Name)
	if namespace == "/" {
		namespace = "unknown"
	}

	console := vm.NewObject()
	_ = console.Set("log", h.createRuntimeConsoleMethod(namespace, "log"))
	_ = console.Set("info", h.createRuntimeConsoleMethod(namespace, "info"))
	_ = console.Set("warn", h.createRuntimeConsoleMethod(namespace, "warn"))
	_ = console.Set("error", h.createRuntimeConsoleMethod(namespace, "error"))
	_ = console.Set("debug", h.createRuntimeConsoleMethod(namespace, "debug"))
	_ = vm.Set("console", console)
	_ = vm.Set("logger", console)

	_ = vm.Set("isIPv4", func(ip string) bool {
		parsed := net.ParseIP(strings.TrimSpace(ip))
		return parsed != nil && parsed.To4() != nil
	})
	_ = vm.Set("isIPv6", func(ip string) bool {
		parsed := net.ParseIP(strings.TrimSpace(ip))
		return parsed != nil && parsed.To4() == nil
	})

	if _, err := vm.RunString(urlSearchParamsPolyfill); err != nil {
		return &runtimeExecError{
			Status:  http.StatusInternalServerError,
			Message: "failed to initialize URLSearchParams polyfill",
		}
	}

	_ = vm.Set("require", func(call goja.FunctionCall) goja.Value {
		moduleName := strings.TrimSpace(call.Argument(0).String())
		switch moduleName {
		case "url", "node:url":
			out := vm.NewObject()
			_ = out.Set("URLSearchParams", vm.Get("__mx_URLSearchParams"))
			return out
		default:
			h.throwJS(vm, http.StatusInternalServerError, fmt.Sprintf("module %q is not allowed", moduleName))
			return goja.Undefined()
		}
	})

	contextObj := vm.NewObject()
	resObj := vm.NewObject()

	throwsFn := func(call goja.FunctionCall) goja.Value {
		status := int(call.Argument(0).ToInteger())
		if status <= 0 {
			status = http.StatusInternalServerError
		}
		message := strings.TrimSpace(call.Argument(1).String())
		if message == "" {
			message = http.StatusText(status)
		}
		h.throwJS(vm, status, message)
		return goja.Undefined()
	}

	_ = resObj.Set("status", func(call goja.FunctionCall) goja.Value {
		code := int(call.Argument(0).ToInteger())
		if code > 0 {
			meta.StatusCode = code
		}
		return resObj
	})
	_ = resObj.Set("type", func(call goja.FunctionCall) goja.Value {
		meta.ContentType = strings.TrimSpace(call.Argument(0).String())
		return resObj
	})
	_ = resObj.Set("send", func(call goja.FunctionCall) goja.Value {
		data, hasData := exportJSValueWithPresence(call.Argument(0))
		meta.Sent = true
		meta.SentData = data
		meta.SentHasData = hasData
		return call.Argument(0)
	})
	_ = resObj.Set("json", func(call goja.FunctionCall) goja.Value {
		data, hasData := exportJSValueWithPresence(call.Argument(0))
		meta.ContentType = "application/json; charset=utf-8"
		meta.Sent = true
		meta.SentData = data
		meta.SentHasData = hasData
		return call.Argument(0)
	})
	_ = resObj.Set("throws", throwsFn)

	_ = contextObj.Set("req", ctx.Req)
	_ = contextObj.Set("res", resObj)
	_ = contextObj.Set("query", ctx.Query)
	_ = contextObj.Set("headers", ctx.Headers)
	_ = contextObj.Set("params", ctx.Params)
	_ = contextObj.Set("method", ctx.Method)
	_ = contextObj.Set("path", ctx.Path)
	_ = contextObj.Set("url", ctx.URL)
	_ = contextObj.Set("ip", ctx.IP)
	_ = contextObj.Set("body", ctx.Body)
	_ = contextObj.Set("isAuthenticated", ctx.IsAuthenticated)
	_ = contextObj.Set("secret", ctx.Secret)
	_ = contextObj.Set("model", ctx.Model)
	_ = contextObj.Set("name", snippet.Name)
	_ = contextObj.Set("reference", snippet.Reference)
	_ = contextObj.Set("document", ctx.Model)
	_ = contextObj.Set("throws", throwsFn)
	_ = contextObj.Set("status", resObj.Get("status"))

	storageObj := vm.NewObject()
	cacheObj := vm.NewObject()
	dbObj := vm.NewObject()

	_ = cacheObj.Set("get", func(call goja.FunctionCall) goja.Value {
		key := strings.TrimSpace(call.Argument(0).String())
		return h.resolvedPromise(vm, h.cacheGet(namespace, key))
	})
	_ = cacheObj.Set("set", func(call goja.FunctionCall) goja.Value {
		key := strings.TrimSpace(call.Argument(0).String())
		value := exportJSValue(call.Argument(1))
		ttl := int64(call.Argument(2).ToInteger())
		h.cacheSet(namespace, key, value, ttl)
		return h.resolvedPromise(vm, nil)
	})
	_ = cacheObj.Set("del", func(call goja.FunctionCall) goja.Value {
		key := strings.TrimSpace(call.Argument(0).String())
		h.cacheDel(namespace, key)
		return h.resolvedPromise(vm, nil)
	})

	_ = dbObj.Set("get", func(call goja.FunctionCall) goja.Value {
		key := strings.TrimSpace(call.Argument(0).String())
		return h.resolvedPromise(vm, h.storageGet(namespace, key))
	})
	_ = dbObj.Set("find", func(call goja.FunctionCall) goja.Value {
		condition := exportJSValue(call.Argument(0))
		return h.resolvedPromise(vm, h.storageFind(namespace, condition))
	})
	_ = dbObj.Set("set", func(call goja.FunctionCall) goja.Value {
		key := strings.TrimSpace(call.Argument(0).String())
		value := exportJSValue(call.Argument(1))
		h.storageSet(namespace, key, value)
		return h.resolvedPromise(vm, nil)
	})
	_ = dbObj.Set("insert", func(call goja.FunctionCall) goja.Value {
		key := strings.TrimSpace(call.Argument(0).String())
		value := exportJSValue(call.Argument(1))
		if err := h.storageInsert(namespace, key, value); err != nil {
			return h.rejectedPromise(vm, map[string]interface{}{"message": err.Error()})
		}
		return h.resolvedPromise(vm, nil)
	})
	_ = dbObj.Set("update", func(call goja.FunctionCall) goja.Value {
		key := strings.TrimSpace(call.Argument(0).String())
		value := exportJSValue(call.Argument(1))
		if err := h.storageUpdate(namespace, key, value); err != nil {
			return h.rejectedPromise(vm, map[string]interface{}{"message": err.Error()})
		}
		return h.resolvedPromise(vm, nil)
	})
	_ = dbObj.Set("del", func(call goja.FunctionCall) goja.Value {
		key := strings.TrimSpace(call.Argument(0).String())
		h.storageDel(namespace, key)
		return h.resolvedPromise(vm, nil)
	})

	_ = storageObj.Set("cache", cacheObj)
	_ = storageObj.Set("db", dbObj)
	_ = contextObj.Set("storage", storageObj)

	_ = contextObj.Set("getService", func(call goja.FunctionCall) goja.Value {
		serviceName := strings.TrimSpace(call.Argument(0).String())
		switch serviceName {
		case "http":
			return h.resolvedPromise(vm, h.createHTTPService(vm))
		case "config":
			return h.resolvedPromise(vm, h.createConfigService(vm))
		default:
			return h.rejectedPromise(vm, map[string]interface{}{
				"message": fmt.Sprintf("service %q is not available", serviceName),
			})
		}
	})

	_ = contextObj.Set("getMaster", func(goja.FunctionCall) goja.Value {
		master, err := h.loadMasterUser()
		if err != nil {
			return h.rejectedPromise(vm, map[string]interface{}{"message": err.Error()})
		}
		return h.resolvedPromise(vm, master)
	})

	_ = contextObj.Set("broadcast", func(call goja.FunctionCall) goja.Value {
		eventType := strings.TrimSpace(call.Argument(0).String())
		payload := exportJSValue(call.Argument(1))
		h.broadcastServerlessEvent(eventType, payload)
		return h.resolvedPromise(vm, nil)
	})
	_ = contextObj.Set("writeAsset", func(call goja.FunctionCall) goja.Value {
		assetPath := call.Argument(0).String()
		data := exportJSValue(call.Argument(1))
		options := exportJSValue(call.Argument(2))
		if err := h.writeAsset(assetPath, data, options); err != nil {
			return h.rejectedPromise(vm, map[string]interface{}{"message": err.Error()})
		}
		return h.resolvedPromise(vm, nil)
	})
	_ = contextObj.Set("readAsset", func(call goja.FunctionCall) goja.Value {
		assetPath := call.Argument(0).String()
		options := exportJSValue(call.Argument(1))
		content, err := h.readAsset(assetPath, options)
		if err != nil {
			return h.rejectedPromise(vm, map[string]interface{}{"message": err.Error()})
		}
		return h.resolvedPromise(vm, content)
	})

	_ = vm.Set("context", contextObj)
	_ = vm.Set("__mx_context", contextObj)
	_ = vm.Set("secret", ctx.Secret)

	return nil
}

func (h *Handler) resolveResultValue(value goja.Value) (interface{}, bool, error) {
	if value == nil || goja.IsUndefined(value) {
		return nil, false, nil
	}
	if goja.IsNull(value) {
		return nil, true, nil
	}

	if p, ok := value.Export().(*goja.Promise); ok {
		switch p.State() {
		case goja.PromiseStatePending:
			return nil, false, &runtimeExecError{
				Status:  http.StatusInternalServerError,
				Message: "serverless function returned a pending promise",
			}
		case goja.PromiseStateRejected:
			message, status := parseRuntimeErrorValue(p.Result())
			if status == 0 {
				status = http.StatusInternalServerError
			}
			return nil, false, &runtimeExecError{Status: status, Message: message}
		default:
			return h.resolveResultValue(p.Result())
		}
	}

	return exportJSValue(value), true, nil
}

func (h *Handler) normalizeRuntimeError(err error, timeoutReason string) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		if interrupted.Value() == timeoutReason {
			return &runtimeExecError{
				Status:  http.StatusGatewayTimeout,
				Message: "serverless function execution timeout",
			}
		}
		return &runtimeExecError{
			Status:  http.StatusInternalServerError,
			Message: "serverless execution interrupted",
		}
	}

	var exception *goja.Exception
	if errors.As(err, &exception) {
		message, status := parseRuntimeErrorValue(exception.Value())
		if status == 0 {
			status = http.StatusInternalServerError
		}
		return &runtimeExecError{
			Status:  status,
			Message: message,
		}
	}

	return &runtimeExecError{
		Status:  http.StatusInternalServerError,
		Message: err.Error(),
	}
}

func parseRuntimeErrorValue(value goja.Value) (string, int) {
	if value == nil || goja.IsNull(value) || goja.IsUndefined(value) {
		return "unknown runtime error", 0
	}

	exported := value.Export()
	switch v := exported.(type) {
	case string:
		return v, 0
	case error:
		return v.Error(), 0
	case map[string]interface{}:
		msg := toString(v["message"])
		if msg == "" {
			msg = toString(v["error"])
		}
		if msg == "" {
			msg = fmt.Sprintf("%v", exported)
		}
		status := toInt(v["status"])
		if status == 0 {
			status = toInt(v["statusCode"])
		}
		return msg, status
	default:
		return fmt.Sprintf("%v", exported), 0
	}
}

func toString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

func toInt(v interface{}) int {
	switch x := v.(type) {
	case int:
		return x
	case int8:
		return int(x)
	case int16:
		return int(x)
	case int32:
		return int(x)
	case int64:
		return int(x)
	case uint:
		return int(x)
	case uint8:
		return int(x)
	case uint16:
		return int(x)
	case uint32:
		return int(x)
	case uint64:
		return int(x)
	case float32:
		return int(x)
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(x))
		return n
	default:
		return 0
	}
}

func exportJSValue(value goja.Value) interface{} {
	out, _ := exportJSValueWithPresence(value)
	return out
}

func exportJSValueWithPresence(value goja.Value) (interface{}, bool) {
	if value == nil || goja.IsUndefined(value) {
		return nil, false
	}
	if goja.IsNull(value) {
		return nil, true
	}
	return value.Export(), true
}

func exportMapValue(value goja.Value) map[string]interface{} {
	if value == nil || goja.IsNull(value) || goja.IsUndefined(value) {
		return nil
	}
	exported := value.Export()
	asMap, ok := exported.(map[string]interface{})
	if !ok {
		return nil
	}
	return asMap
}

func (h *Handler) createRuntimeConsoleMethod(namespace, level string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		h.runtimeConsolePrint(namespace, level, call.Arguments)
		return goja.Undefined()
	}
}

func (h *Handler) runtimeConsolePrint(namespace, level string, args []goja.Value) {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, fmt.Sprintf("[sandbox:%s]", namespace))
	for _, arg := range args {
		parts = append(parts, runtimeConsoleValueToString(exportJSValue(arg)))
	}
	line := strings.Join(parts, " ")
	switch level {
	case "warn", "error":
		_, _ = fmt.Fprintln(os.Stderr, line)
	default:
		_, _ = fmt.Fprintln(os.Stdout, line)
	}
}

func runtimeConsoleValueToString(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case error:
		return x.Error()
	case []byte:
		return string(x)
	default:
		b, err := json.Marshal(x)
		if err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", x)
	}
}

func (h *Handler) throwJS(vm *goja.Runtime, status int, message string) {
	obj := vm.NewObject()
	_ = obj.Set("message", message)
	_ = obj.Set("status", status)
	panic(obj)
}

func (h *Handler) resolvedPromise(vm *goja.Runtime, value interface{}) goja.Value {
	promise, resolve, _ := vm.NewPromise()
	_ = resolve(value)
	return vm.ToValue(promise)
}

func (h *Handler) rejectedPromise(vm *goja.Runtime, reason interface{}) goja.Value {
	promise, _, reject := vm.NewPromise()
	_ = reject(reason)
	return vm.ToValue(promise)
}

func (h *Handler) writeServerlessResponse(c *gin.Context, out *executorResult) {
	statusCode := http.StatusOK
	if out != nil && out.meta.StatusCode > 0 {
		statusCode = out.meta.StatusCode
	}
	contentType := ""
	if out != nil {
		contentType = strings.TrimSpace(out.meta.ContentType)
	}
	if contentType != "" {
		c.Header("Content-Type", contentType)
	}

	if out == nil || !out.hasData {
		c.Status(statusCode)
		return
	}

	switch payload := out.data.(type) {
	case []byte:
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		c.Data(statusCode, contentType, payload)
	case string:
		c.String(statusCode, payload)
	default:
		c.JSON(statusCode, payload)
	}
}

const urlSearchParamsPolyfill = `
(function (global) {
  function encode(v) {
    return encodeURIComponent(String(v)).replace(/%20/g, '+')
  }
  function decode(v) {
    return decodeURIComponent(String(v).replace(/\+/g, '%20'))
  }
  function URLSearchParams(init) {
    this._pairs = []
    if (!init) return
    if (typeof init === 'string') {
      var source = init.charAt(0) === '?' ? init.slice(1) : init
      if (!source) return
      var items = source.split('&')
      for (var i = 0; i < items.length; i++) {
        var item = items[i]
        if (!item) continue
        var eq = item.indexOf('=')
        if (eq === -1) this.append(decode(item), '')
        else this.append(decode(item.slice(0, eq)), decode(item.slice(eq + 1)))
      }
      return
    }
    if (Array.isArray(init)) {
      for (var j = 0; j < init.length; j++) {
        var pair = init[j]
        if (Array.isArray(pair) && pair.length >= 2) this.append(pair[0], pair[1])
      }
      return
    }
    if (typeof init === 'object') {
      for (var key in init) {
        if (Object.prototype.hasOwnProperty.call(init, key)) this.append(key, init[key])
      }
    }
  }
  URLSearchParams.prototype.append = function (key, value) {
    this._pairs.push([String(key), String(value)])
  }
  URLSearchParams.prototype.toString = function () {
    var out = []
    for (var i = 0; i < this._pairs.length; i++) {
      out.push(encode(this._pairs[i][0]) + '=' + encode(this._pairs[i][1]))
    }
    return out.join('&')
  }
  global.__mx_URLSearchParams = URLSearchParams
})(globalThis)
`
