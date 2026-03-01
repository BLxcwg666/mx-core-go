package serverless

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"
	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"gorm.io/gorm"
)

const serverlessExecutionTimeout = 30 * time.Second
const serverlessCacheKeyPrefix = "mx:serverless:storage:cache:"
const serverlessOnlineAssetBaseURL = "https://cdn.jsdelivr.net/gh/mx-space/assets@master/"

type compiledSnippet struct {
	UpdatedAt time.Time
	Code      string
}

type runtimeResponseMeta struct {
	StatusCode  int
	ContentType string
	Sent        bool
	SentData    interface{}
}

type runtimeContext struct {
	Req             map[string]interface{}
	Query           map[string]interface{}
	Headers         map[string]string
	Params          map[string]interface{}
	Method          string
	Path            string
	URL             string
	IP              string
	Body            interface{}
	IsAuthenticated bool
	Secret          map[string]interface{}
	Model           map[string]interface{}
}

type executorResult struct {
	data interface{}
	meta runtimeResponseMeta
}

type runtimeExecError struct {
	Status  int
	Message string
}

func (e *runtimeExecError) Error() string { return e.Message }

func asRuntimeExecError(err error, target **runtimeExecError) bool {
	return errors.As(err, target)
}

type axiosRequestError struct {
	Message  string
	Response map[string]interface{}
}

func (e *axiosRequestError) Error() string { return e.Message }

func (h *Handler) buildRuntimeContext(c *gin.Context, snippet *models.SnippetModel) runtimeContext {
	query := make(map[string]interface{}, len(c.Request.URL.Query()))
	for k, values := range c.Request.URL.Query() {
		if len(values) == 1 {
			query[k] = values[0]
			continue
		}
		cloned := make([]string, len(values))
		copy(cloned, values)
		query[k] = cloned
	}

	headers := make(map[string]string, len(c.Request.Header))
	for k, values := range c.Request.Header {
		headers[k] = strings.Join(values, ",")
	}

	params := make(map[string]interface{}, len(c.Params))
	for _, p := range c.Params {
		params[p.Key] = p.Value
	}

	body := parseRequestBody(c)
	method := strings.ToUpper(c.Request.Method)
	path := c.Request.URL.Path
	urlText := c.Request.URL.String()
	ip := c.ClientIP()

	req := map[string]interface{}{
		"method":  method,
		"path":    path,
		"query":   query,
		"body":    body,
		"headers": headers,
		"params":  params,
		"url":     urlText,
		"ip":      ip,
	}

	return runtimeContext{
		Req:             req,
		Query:           query,
		Headers:         headers,
		Params:          params,
		Method:          method,
		Path:            path,
		URL:             urlText,
		IP:              ip,
		Body:            body,
		IsAuthenticated: hasFunctionAccess(c),
		Secret:          parseSnippetSecret(snippet.Secret),
		Model: map[string]interface{}{
			"id":        snippet.ID,
			"name":      snippet.Name,
			"reference": snippet.Reference,
		},
	}
}

func parseRequestBody(c *gin.Context) interface{} {
	if c.Request == nil || c.Request.Body == nil {
		return nil
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(raw))
	if len(raw) == 0 {
		return nil
	}

	contentType := strings.ToLower(c.ContentType())
	if strings.Contains(contentType, "application/json") {
		var body interface{}
		if err := json.Unmarshal(raw, &body); err == nil {
			return body
		}
	}
	if strings.Contains(contentType, "application/x-www-form-urlencoded") ||
		strings.Contains(contentType, "multipart/form-data") {
		if err := c.Request.ParseForm(); err == nil {
			return queryValuesToMap(c.Request.Form)
		}
	}

	return string(raw)
}

func parseSnippetSecret(raw string) map[string]interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]interface{}{}
	}

	var asJSON map[string]interface{}
	if json.Unmarshal([]byte(raw), &asJSON) == nil {
		return asJSON
	}

	values, err := url.ParseQuery(raw)
	if err != nil {
		return map[string]interface{}{}
	}

	return queryValuesToMap(values)
}

func queryValuesToMap(values url.Values) map[string]interface{} {
	out := make(map[string]interface{}, len(values))
	for k, v := range values {
		if len(v) == 1 {
			out[k] = v[0]
			continue
		}
		cloned := make([]string, len(v))
		copy(cloned, v)
		out[k] = cloned
	}
	return out
}

func (h *Handler) compileSnippet(snippet *models.SnippetModel) (string, error) {
	h.compiledMu.RLock()
	if cached, ok := h.compiled[snippet.ID]; ok && cached.UpdatedAt.Equal(snippet.UpdatedAt) {
		h.compiledMu.RUnlock()
		return cached.Code, nil
	}
	h.compiledMu.RUnlock()

	result := api.Transform(snippet.Raw, api.TransformOptions{
		Loader:     api.LoaderTS,
		Format:     api.FormatCommonJS,
		Target:     api.ES2020,
		Sourcefile: fmt.Sprintf("%s/%s.ts", snippet.Reference, snippet.Name),
		Charset:    api.CharsetUTF8,
	})
	if len(result.Errors) > 0 {
		return "", fmt.Errorf("transform failed: %s", result.Errors[0].Text)
	}

	code := string(result.Code)
	h.compiledMu.Lock()
	h.compiled[snippet.ID] = compiledSnippet{
		UpdatedAt: snippet.UpdatedAt,
		Code:      code,
	}
	h.compiledMu.Unlock()

	return code, nil
}

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

	result, err := h.resolveResultValue(resultValue)
	if err != nil {
		return nil, err
	}
	if meta.Sent {
		result = meta.SentData
	}

	return &executorResult{
		data: result,
		meta: meta,
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
		meta.Sent = true
		meta.SentData = exportJSValue(call.Argument(0))
		return call.Argument(0)
	})
	_ = resObj.Set("json", func(call goja.FunctionCall) goja.Value {
		meta.ContentType = "application/json; charset=utf-8"
		meta.Sent = true
		meta.SentData = exportJSValue(call.Argument(0))
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

func (h *Handler) resolveResultValue(value goja.Value) (interface{}, error) {
	if value == nil || goja.IsNull(value) || goja.IsUndefined(value) {
		return nil, nil
	}

	if p, ok := value.Export().(*goja.Promise); ok {
		switch p.State() {
		case goja.PromiseStatePending:
			return nil, &runtimeExecError{
				Status:  http.StatusInternalServerError,
				Message: "serverless function returned a pending promise",
			}
		case goja.PromiseStateRejected:
			message, status := parseRuntimeErrorValue(p.Result())
			if status == 0 {
				status = http.StatusInternalServerError
			}
			return nil, &runtimeExecError{Status: status, Message: message}
		default:
			return exportJSValue(p.Result()), nil
		}
	}

	return exportJSValue(value), nil
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
	if value == nil || goja.IsNull(value) || goja.IsUndefined(value) {
		return nil
	}
	return value.Export()
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

func (h *Handler) createHTTPService(vm *goja.Runtime) *goja.Object {
	serviceObj := vm.NewObject()
	axiosObj := vm.NewObject()

	_ = axiosObj.Set("get", func(call goja.FunctionCall) goja.Value {
		return h.axiosPromise(vm, http.MethodGet, call.Argument(0), goja.Undefined(), call.Argument(1))
	})
	_ = axiosObj.Set("delete", func(call goja.FunctionCall) goja.Value {
		return h.axiosPromise(vm, http.MethodDelete, call.Argument(0), goja.Undefined(), call.Argument(1))
	})
	_ = axiosObj.Set("post", func(call goja.FunctionCall) goja.Value {
		return h.axiosPromise(vm, http.MethodPost, call.Argument(0), call.Argument(1), call.Argument(2))
	})
	_ = axiosObj.Set("put", func(call goja.FunctionCall) goja.Value {
		return h.axiosPromise(vm, http.MethodPut, call.Argument(0), call.Argument(1), call.Argument(2))
	})
	_ = axiosObj.Set("patch", func(call goja.FunctionCall) goja.Value {
		return h.axiosPromise(vm, http.MethodPatch, call.Argument(0), call.Argument(1), call.Argument(2))
	})
	_ = axiosObj.Set("request", func(call goja.FunctionCall) goja.Value {
		cfg := exportMapValue(call.Argument(0))
		if cfg == nil {
			return h.rejectedPromise(vm, map[string]interface{}{"message": "request config is required"})
		}
		method := strings.ToUpper(strings.TrimSpace(toString(cfg["method"])))
		if method == "" {
			method = http.MethodGet
		}
		rawURL := strings.TrimSpace(toString(cfg["url"]))
		if rawURL == "" {
			return h.rejectedPromise(vm, map[string]interface{}{"message": "request url is required"})
		}
		return h.axiosPromiseFromParts(vm, method, rawURL, cfg["data"], cfg)
	})

	_ = serviceObj.Set("axios", axiosObj)
	return serviceObj
}

func (h *Handler) axiosPromise(
	vm *goja.Runtime,
	method string,
	urlValue goja.Value,
	dataValue goja.Value,
	configValue goja.Value,
) goja.Value {
	rawURL := strings.TrimSpace(urlValue.String())
	if rawURL == "" {
		return h.rejectedPromise(vm, map[string]interface{}{"message": "request url is required"})
	}

	var data interface{}
	if !goja.IsNull(dataValue) && !goja.IsUndefined(dataValue) {
		data = exportJSValue(dataValue)
	}
	config := exportMapValue(configValue)
	return h.axiosPromiseFromParts(vm, method, rawURL, data, config)
}

func (h *Handler) axiosPromiseFromParts(
	vm *goja.Runtime,
	method string,
	rawURL string,
	data interface{},
	config map[string]interface{},
) goja.Value {
	result, err := h.doAxiosRequest(method, rawURL, data, config)
	if err != nil {
		if reqErr, ok := err.(*axiosRequestError); ok {
			return h.rejectedPromise(vm, map[string]interface{}{
				"message":  reqErr.Message,
				"response": reqErr.Response,
			})
		}
		return h.rejectedPromise(vm, map[string]interface{}{"message": err.Error()})
	}
	return h.resolvedPromise(vm, result)
}

func (h *Handler) doAxiosRequest(
	method string,
	rawURL string,
	data interface{},
	config map[string]interface{},
) (map[string]interface{}, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, fmt.Errorf("invalid request url: %s", rawURL)
	}

	if config != nil {
		if params, ok := config["params"]; ok {
			mergeURLParams(parsedURL, params)
		}
	}

	headers := map[string]string{}
	if config != nil {
		if headerRaw, ok := config["headers"]; ok {
			for k, v := range toStringMap(headerRaw) {
				headers[k] = v
			}
		}
	}

	var bodyReader io.Reader
	if method != http.MethodGet && method != http.MethodDelete && data != nil {
		switch typed := data.(type) {
		case string:
			bodyReader = strings.NewReader(typed)
			if _, has := headers["Content-Type"]; !has {
				headers["Content-Type"] = "text/plain; charset=utf-8"
			}
		case []byte:
			bodyReader = bytes.NewReader(typed)
			if _, has := headers["Content-Type"]; !has {
				headers["Content-Type"] = "application/octet-stream"
			}
		default:
			payload, err := json.Marshal(typed)
			if err != nil {
				return nil, err
			}
			bodyReader = bytes.NewReader(payload)
			if _, has := headers["Content-Type"]; !has {
				headers["Content-Type"] = "application/json; charset=utf-8"
			}
		}
	}

	req, err := http.NewRequest(method, parsedURL.String(), bodyReader)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	result := map[string]interface{}{
		"status":  resp.StatusCode,
		"headers": headerToMap(resp.Header),
		"data":    parseHTTPBody(resp.Header.Get("Content-Type"), bodyBytes),
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, &axiosRequestError{
			Message:  fmt.Sprintf("request failed with status %d", resp.StatusCode),
			Response: result,
		}
	}

	return result, nil
}

func mergeURLParams(target *url.URL, params interface{}) {
	if target == nil || params == nil {
		return
	}

	query := target.Query()
	switch p := params.(type) {
	case map[string]interface{}:
		for k, v := range p {
			switch vv := v.(type) {
			case []interface{}:
				for _, item := range vv {
					query.Add(k, toString(item))
				}
			case []string:
				for _, item := range vv {
					query.Add(k, item)
				}
			default:
				query.Add(k, toString(vv))
			}
		}
	case map[string]string:
		for k, v := range p {
			query.Add(k, v)
		}
	}
	target.RawQuery = query.Encode()
}

func toStringMap(v interface{}) map[string]string {
	out := map[string]string{}
	switch m := v.(type) {
	case map[string]interface{}:
		for k, value := range m {
			out[k] = toString(value)
		}
	case map[string]string:
		for k, value := range m {
			out[k] = value
		}
	}
	return out
}

func parseHTTPBody(contentType string, raw []byte) interface{} {
	if len(raw) == 0 {
		return nil
	}
	if strings.Contains(strings.ToLower(contentType), "application/json") {
		var out interface{}
		if err := json.Unmarshal(raw, &out); err == nil {
			return out
		}
	}
	return string(raw)
}

func headerToMap(header http.Header) map[string]interface{} {
	out := make(map[string]interface{}, len(header))
	for k, values := range header {
		if len(values) == 1 {
			out[k] = values[0]
			continue
		}
		cloned := make([]string, len(values))
		copy(cloned, values)
		out[k] = cloned
	}
	return out
}

func (h *Handler) createConfigService(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("get", func(call goja.FunctionCall) goja.Value {
		key := strings.TrimSpace(call.Argument(0).String())
		value, err := h.getConfigValue(key)
		if err != nil {
			return h.rejectedPromise(vm, map[string]interface{}{"message": err.Error()})
		}
		return h.resolvedPromise(vm, value)
	})
	return obj
}

func (h *Handler) getConfigValue(key string) (interface{}, error) {
	if key == "" {
		return nil, nil
	}

	var opt models.OptionModel
	if err := h.db.Where("name = ?", "configs").First(&opt).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(opt.Value), &cfg); err != nil {
		return nil, err
	}

	if value, ok := cfg[key]; ok {
		return withCamelCaseAliases(value), nil
	}

	snakeKey := camelToSnake(key)
	if value, ok := cfg[snakeKey]; ok {
		return withCamelCaseAliases(value), nil
	}
	return nil, nil
}

func camelToSnake(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteByte(byte(r + ('a' - 'A')))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func snakeToCamel(s string) string {
	if s == "" {
		return s
	}

	parts := strings.Split(s, "_")
	if len(parts) == 1 {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	b.WriteString(parts[0])
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		if len(part) > 1 {
			b.WriteString(part[1:])
		}
	}
	return b.String()
}

func withCamelCaseAliases(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v)*2)
		for key, child := range v {
			normalizedChild := withCamelCaseAliases(child)
			out[key] = normalizedChild

			camelKey := snakeToCamel(key)
			if camelKey == key {
				continue
			}
			if _, exists := out[camelKey]; !exists {
				out[camelKey] = normalizedChild
			}
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i := range v {
			out[i] = withCamelCaseAliases(v[i])
		}
		return out
	default:
		return value
	}
}

func (h *Handler) loadMasterUser() (interface{}, error) {
	var user models.UserModel
	if err := h.db.Order("created_at ASC").First(&user).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}

	return map[string]interface{}{
		"id":            user.ID,
		"username":      user.Username,
		"name":          user.Name,
		"introduce":     user.Introduce,
		"avatar":        user.Avatar,
		"mail":          user.Mail,
		"url":           user.URL,
		"lastLoginTime": user.LastLoginTime,
		"lastLoginIp":   user.LastLoginIP,
	}, nil
}

func (h *Handler) cacheGet(namespace, key string) interface{} {
	if key == "" {
		return nil
	}
	cacheKey := serverlessCacheKeyPrefix + namespace + ":" + key

	raw, err := h.rc.Get(context.Background(), cacheKey)
	if err != nil || raw == "" {
		return nil
	}
	return decodeStorageValue(raw)
}

func (h *Handler) cacheSet(namespace, key string, value interface{}, ttlSeconds int64) {
	if key == "" {
		return
	}
	cacheKey := serverlessCacheKeyPrefix + namespace + ":" + key
	if ttlSeconds <= 0 {
		ttlSeconds = int64((7 * 24 * time.Hour).Seconds())
	}

	_ = h.rc.Set(
		context.Background(),
		cacheKey,
		encodeStorageValue(value),
		time.Duration(ttlSeconds)*time.Second,
	)
}

func (h *Handler) cacheDel(namespace, key string) {
	if key == "" {
		return
	}
	cacheKey := serverlessCacheKeyPrefix + namespace + ":" + key

	_ = h.rc.Del(context.Background(), cacheKey)
}

func (h *Handler) storageGet(namespace, key string) interface{} {
	if key == "" {
		return nil
	}
	var item models.ServerlessStorageModel
	err := h.db.
		Where("namespace = ? AND `key` = ?", namespace, key).
		First(&item).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return nil
	}
	return decodeStorageValue(item.Value)
}

func (h *Handler) storageFind(namespace string, condition interface{}) interface{} {
	keyFilter := ""
	if cond, ok := condition.(map[string]interface{}); ok {
		keyFilter = strings.TrimSpace(toString(cond["key"]))
	}

	tx := h.db.
		Model(&models.ServerlessStorageModel{}).
		Where("namespace = ?", namespace).
		Order("created_at DESC")
	if keyFilter != "" {
		tx = tx.Where("`key` = ?", keyFilter)
	}

	var items []models.ServerlessStorageModel
	if err := tx.Find(&items).Error; err != nil {
		return []interface{}{}
	}

	out := make([]interface{}, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]interface{}{
			"id":    item.ID,
			"key":   item.Key,
			"value": decodeStorageValue(item.Value),
		})
	}
	return out
}

func (h *Handler) storageSet(namespace, key string, value interface{}) {
	if key == "" {
		return
	}
	encoded := encodeStorageValue(value)

	var existing models.ServerlessStorageModel
	err := h.db.
		Where("namespace = ? AND `key` = ?", namespace, key).
		First(&existing).Error
	if err == nil {
		_ = h.db.Model(&existing).Update("value", encoded).Error
		return
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return
	}

	record := models.ServerlessStorageModel{
		Namespace: namespace,
		Key:       key,
		Value:     encoded,
	}
	_ = h.db.Create(&record).Error
}

func (h *Handler) storageInsert(namespace, key string, value interface{}) error {
	if key == "" {
		return errors.New("key is required")
	}

	var existing models.ServerlessStorageModel
	err := h.db.
		Where("namespace = ? AND `key` = ?", namespace, key).
		First(&existing).Error
	if err == nil {
		return errors.New("key already exists")
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}

	record := models.ServerlessStorageModel{
		Namespace: namespace,
		Key:       key,
		Value:     encodeStorageValue(value),
	}
	return h.db.Create(&record).Error
}

func (h *Handler) storageUpdate(namespace, key string, value interface{}) error {
	if key == "" {
		return errors.New("key is required")
	}

	var existing models.ServerlessStorageModel
	err := h.db.
		Where("namespace = ? AND `key` = ?", namespace, key).
		First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		return errors.New("key not exists")
	}
	if err != nil {
		return err
	}
	return h.db.Model(&existing).Update("value", encodeStorageValue(value)).Error
}

func (h *Handler) storageDel(namespace, key string) {
	if key == "" {
		return
	}
	_ = h.db.
		Where("namespace = ? AND `key` = ?", namespace, key).
		Delete(&models.ServerlessStorageModel{}).Error
}

func encodeStorageValue(v interface{}) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", v)
}

func decodeStorageValue(raw string) interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal([]byte(raw), &v); err == nil {
		return v
	}
	return raw
}

func (h *Handler) writeAsset(rawPath string, data interface{}, options interface{}) error {
	relativePath, err := safeAssetRelativePath(rawPath)
	if err != nil {
		return err
	}

	fullPath, err := resolveAssetPath(resolveServerlessUserAssetDir(), relativePath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return err
	}

	payload, err := normalizeAssetWriteData(data, parseAssetEncoding(options))
	if err != nil {
		return err
	}

	flags, err := parseAssetWriteFlag(options)
	if err != nil {
		return err
	}

	fileMode := parseAssetWriteMode(options)
	file, err := os.OpenFile(fullPath, flags, fileMode)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := file.Write(payload); err != nil {
		return err
	}

	return nil
}

func (h *Handler) readAsset(rawPath string, options interface{}) (interface{}, error) {
	relativePath, err := safeAssetRelativePath(rawPath)
	if err != nil {
		return nil, err
	}

	userAssetPath, err := resolveAssetPath(resolveServerlessUserAssetDir(), relativePath)
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(userAssetPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}

		embedAssetPath, pathErr := resolveAssetPath(resolveServerlessEmbedAssetDir(), relativePath)
		if pathErr != nil {
			return nil, pathErr
		}
		raw, err = os.ReadFile(embedAssetPath)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}

			raw, err = h.fetchOnlineAsset(relativePath)
			if err != nil {
				return nil, err
			}
			if mkErr := os.MkdirAll(filepath.Dir(embedAssetPath), 0o755); mkErr == nil {
				_ = os.WriteFile(embedAssetPath, raw, 0o644)
			}
		}
	}

	encoding := parseAssetEncoding(options)
	if encoding == "" || encoding == "buffer" {
		return raw, nil
	}

	switch encoding {
	case "utf8", "utf-8", "ascii", "latin1", "binary", "utf16le", "utf-16le":
		return string(raw), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(raw), nil
	case "hex":
		return hex.EncodeToString(raw), nil
	default:
		return nil, fmt.Errorf("unsupported readAsset encoding %q", encoding)
	}
}

func (h *Handler) fetchOnlineAsset(relativePath string) ([]byte, error) {
	urlPath := strings.TrimPrefix(strings.ReplaceAll(relativePath, "\\", "/"), "/")
	targetURL := serverlessOnlineAssetBaseURL + urlPath

	resp, err := h.httpClient.Get(targetURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("asset %q not found", relativePath)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func parseAssetWriteFlag(options interface{}) (int, error) {
	flag := "w"
	if cfg, ok := options.(map[string]interface{}); ok {
		if raw := strings.TrimSpace(strings.ToLower(toString(cfg["flag"]))); raw != "" {
			flag = raw
		}
	}

	switch flag {
	case "w":
		return os.O_CREATE | os.O_WRONLY | os.O_TRUNC, nil
	case "w+":
		return os.O_CREATE | os.O_RDWR | os.O_TRUNC, nil
	case "wx", "xw":
		return os.O_CREATE | os.O_WRONLY | os.O_TRUNC | os.O_EXCL, nil
	case "wx+", "xw+":
		return os.O_CREATE | os.O_RDWR | os.O_TRUNC | os.O_EXCL, nil
	case "a":
		return os.O_CREATE | os.O_WRONLY | os.O_APPEND, nil
	case "a+":
		return os.O_CREATE | os.O_RDWR | os.O_APPEND, nil
	case "ax", "xa":
		return os.O_CREATE | os.O_WRONLY | os.O_APPEND | os.O_EXCL, nil
	case "ax+", "xa+":
		return os.O_CREATE | os.O_RDWR | os.O_APPEND | os.O_EXCL, nil
	default:
		return 0, fmt.Errorf("unsupported writeAsset flag %q", flag)
	}
}

func parseAssetWriteMode(options interface{}) os.FileMode {
	const defaultMode = 0o644
	cfg, ok := options.(map[string]interface{})
	if !ok {
		return defaultMode
	}

	modeRaw, exists := cfg["mode"]
	if !exists || modeRaw == nil {
		return defaultMode
	}

	switch v := modeRaw.(type) {
	case int:
		return os.FileMode(v)
	case int8:
		return os.FileMode(v)
	case int16:
		return os.FileMode(v)
	case int32:
		return os.FileMode(v)
	case int64:
		return os.FileMode(v)
	case uint:
		return os.FileMode(v)
	case uint8:
		return os.FileMode(v)
	case uint16:
		return os.FileMode(v)
	case uint32:
		return os.FileMode(v)
	case uint64:
		return os.FileMode(v)
	case float32:
		return os.FileMode(int64(v))
	case float64:
		return os.FileMode(int64(v))
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return defaultMode
		}
		parsed, err := strconv.ParseInt(trimmed, 0, 64)
		if err != nil {
			return defaultMode
		}
		return os.FileMode(parsed)
	default:
		return defaultMode
	}
}

func normalizeAssetWriteData(data interface{}, encoding string) ([]byte, error) {
	switch value := data.(type) {
	case nil:
		return []byte{}, nil
	case string:
		return encodeAssetString(value, encoding)
	case []byte:
		cloned := make([]byte, len(value))
		copy(cloned, value)
		return cloned, nil
	case []interface{}:
		if b, ok := tryConvertToByteSlice(value); ok {
			return b, nil
		}
	}

	if marshaled, err := json.Marshal(data); err == nil {
		return marshaled, nil
	}
	return []byte(fmt.Sprintf("%v", data)), nil
}

func tryConvertToByteSlice(arr []interface{}) ([]byte, bool) {
	if len(arr) == 0 {
		return []byte{}, true
	}
	out := make([]byte, len(arr))
	for i := range arr {
		n := toInt(arr[i])
		if n < 0 || n > 255 {
			return nil, false
		}
		out[i] = byte(n)
	}
	return out, true
}

func encodeAssetString(content, encoding string) ([]byte, error) {
	switch encoding {
	case "", "utf8", "utf-8", "ascii", "latin1", "binary", "buffer", "utf16le", "utf-16le":
		return []byte(content), nil
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	case "hex":
		decoded, err := hex.DecodeString(content)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("unsupported writeAsset encoding %q", encoding)
	}
}

func parseAssetEncoding(options interface{}) string {
	switch value := options.(type) {
	case nil:
		return ""
	case string:
		return normalizeAssetEncoding(value)
	case map[string]interface{}:
		return normalizeAssetEncoding(toString(value["encoding"]))
	default:
		return ""
	}
}

func normalizeAssetEncoding(raw string) string {
	return strings.TrimSpace(strings.ToLower(raw))
}

func safeAssetRelativePath(rawPath string) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", errors.New("asset path is required")
	}

	sanitized := strings.ReplaceAll(rawPath, "\\", "/")
	parts := strings.Split(sanitized, "/")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." {
			continue
		}
		if isUnsafeAssetSegment(part) {
			part = "."
		}
		if part == "." {
			continue
		}
		cleaned = append(cleaned, part)
	}

	if len(cleaned) == 0 {
		return "", errors.New("asset path is required")
	}
	return filepath.Join(cleaned...), nil
}

func isUnsafeAssetSegment(segment string) bool {
	if segment == "~" {
		return true
	}
	if len(segment) < 2 {
		return false
	}
	for _, r := range segment {
		if r != '.' {
			return false
		}
	}
	return true
}

func resolveAssetPath(rootDir, relativePath string) (string, error) {
	root := filepath.Clean(rootDir)
	target := filepath.Clean(filepath.Join(root, relativePath))
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("asset path escapes root directory")
	}
	return target, nil
}

func resolveServerlessUserAssetDir() string {
	if customDir := strings.TrimSpace(os.Getenv("MX_USER_ASSET_DIR")); customDir != "" {
		return filepath.Clean(customDir)
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("NODE_ENV")), "development") {
		return filepath.Join(".", "tmp", "assets")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".mx-space", "assets")
	}
	return filepath.Join(".", "assets")
}

func resolveServerlessEmbedAssetDir() string {
	return filepath.Join(".", "assets")
}

func (h *Handler) broadcastServerlessEvent(eventType string, payload interface{}) {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" || h.hub == nil {
		return
	}
	eventName := "fn#" + eventType
	h.hub.BroadcastPublic(eventName, payload)
	h.hub.BroadcastAdmin(eventName, payload)
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

	if out == nil || out.data == nil {
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
