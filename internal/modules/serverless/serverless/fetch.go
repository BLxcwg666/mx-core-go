package serverless

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/dop251/goja"
	"github.com/mx-space/core/internal/models"
	"gorm.io/gorm"
)

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

func (h *Handler) broadcastServerlessEvent(eventType string, payload interface{}) {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" || h.hub == nil {
		return
	}
	eventName := "fn#" + eventType
	h.hub.BroadcastPublic(eventName, payload)
	h.hub.BroadcastAdmin(eventName, payload)
}
