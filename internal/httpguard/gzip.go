package httpguard

import (
	"compress/gzip"
	"mime"
	"net/http"
	"strings"
	"sync"
)

var gzipPool = sync.Pool{New: func() any { return gzip.NewWriter(nil) }}

// Gzip compresses text responses (HTML, JS, CSS, JSON, SVG, ...) for clients that
// accept it. It leaves alone SSE, range requests, HEAD, already-encoded bodies
// and non-text types. The decision is made on the first write, once the handler
// has set Content-Type.
func Gzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.Header.Get("Range") != "" ||
			!strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	zw      *gzip.Writer
	decided bool
}

func compressibleType(contentType string) bool {
	mt, _, _ := mime.ParseMediaType(contentType)
	switch {
	case strings.HasPrefix(mt, "text/"):
		return mt != "text/event-stream"
	case strings.HasSuffix(mt, "+json"), strings.HasSuffix(mt, "+xml"):
		return true
	}
	switch mt {
	case "application/json", "application/javascript", "application/xml", "image/svg+xml":
		return true
	}
	return false
}

func (g *gzipResponseWriter) decide(status int, first []byte) {
	g.decided = true
	h := g.Header()
	ct := h.Get("Content-Type")
	if ct == "" && len(first) > 0 {
		ct = http.DetectContentType(first)
	}
	if !compressibleType(ct) {
		return
	}
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", ct) // net/http skips its own sniffing once Content-Encoding is set
	}
	h.Add("Vary", "Accept-Encoding")
	if h.Get("Content-Encoding") != "" || h.Get("Content-Range") != "" ||
		status == http.StatusNoContent || status == http.StatusNotModified || status == http.StatusPartialContent {
		return
	}
	h.Set("Content-Encoding", "gzip")
	h.Del("Content-Length")
	if etag := h.Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
		h.Set("ETag", "W/"+etag) // the encoded bytes differ from the strong validator's
	}
	g.zw = gzipPool.Get().(*gzip.Writer)
	g.zw.Reset(g.ResponseWriter)
}

func (g *gzipResponseWriter) WriteHeader(status int) {
	if !g.decided {
		g.decide(status, nil)
	}
	g.ResponseWriter.WriteHeader(status)
}

func (g *gzipResponseWriter) Write(p []byte) (int, error) {
	if !g.decided {
		g.decide(http.StatusOK, p)
	}
	if g.zw != nil {
		return g.zw.Write(p)
	}
	return g.ResponseWriter.Write(p)
}

func (g *gzipResponseWriter) Flush() {
	if !g.decided { // Flush sends headers, so the encoding must be settled first
		g.decide(http.StatusOK, nil)
	}
	if g.zw != nil {
		_ = g.zw.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (g *gzipResponseWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *gzipResponseWriter) close() {
	if g.zw != nil {
		_ = g.zw.Close()
		gzipPool.Put(g.zw)
	}
}
