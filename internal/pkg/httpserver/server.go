package httpserver

import (
	"net/http"
	"time"
)

const (
	readHeaderTimeout = 3 * time.Second
	readTimeout       = 45 * time.Second
	idleTimeout       = 90 * time.Second
	maxHeaderBytes    = 1 << 20
)

// New returns an HTTP server with conservative defaults that are friendly to
// streaming handlers while still reducing slow-header and oversized-header abuse.
func New(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}
