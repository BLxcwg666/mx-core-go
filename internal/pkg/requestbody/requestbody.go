package requestbody

import (
	"bytes"
	"io"

	"github.com/gin-gonic/gin"
)

const rawBodyContextKey = "mx.request_body.raw"

// Read returns the raw request body and restores Request.Body for later reads.
func Read(c *gin.Context) ([]byte, error) {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return nil, nil
	}

	if cached, ok := c.Get(rawBodyContextKey); ok {
		if raw, ok := cached.([]byte); ok {
			return raw, nil
		}
	}

	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}

	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	c.Set(rawBodyContextKey, raw)
	return raw, nil
}
