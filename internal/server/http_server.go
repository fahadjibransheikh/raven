package server

import (
	"net/http"
	"time"
)

// newHTTPServer sets the timeouts Go leaves off by default. Without them a
// client can hold connections open by sending its request headers very
// slowly, or by leaving idle connections open, until the server runs out.
// Request bodies and responses have no overall limit: uploads can be large
// and the live event stream stays open.
func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
}
