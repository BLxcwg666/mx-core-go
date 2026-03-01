package serverless

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
)

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
