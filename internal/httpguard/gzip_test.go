package httpguard

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func gzipDo(t *testing.T, h http.HandlerFunc, method string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/x", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	Gzip(h).ServeHTTP(rec, req)
	return rec
}

func TestGzipCompressesTextAndSkipsTheRest(t *testing.T) {
	body := strings.Repeat("hello raven ", 200)
	typed := func(ct string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ct)
			w.Header().Set("Content-Length", "2400")
			io.WriteString(w, body)
		}
	}

	rec := gzipDo(t, typed("text/html; charset=utf-8"), "GET", nil)
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Content-Length") != "" || rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("html not compressed: %v", rec.Header())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(zr); string(got) != body {
		t.Fatal("gzip round trip mismatch")
	}

	for _, ct := range []string{"application/json", "application/javascript", "image/svg+xml", "text/css"} {
		if gzipDo(t, typed(ct), "GET", nil).Header().Get("Content-Encoding") != "gzip" {
			t.Errorf("%s not compressed", ct)
		}
	}
	for _, ct := range []string{"text/event-stream", "image/png", "application/zip", "application/pdf"} {
		if gzipDo(t, typed(ct), "GET", nil).Header().Get("Content-Encoding") != "" {
			t.Errorf("%s must not be compressed", ct)
		}
	}
	if gzipDo(t, typed("text/html"), "GET", map[string]string{"Range": "bytes=0-9"}).Header().Get("Content-Encoding") != "" {
		t.Error("range request must not be compressed")
	}
	if gzipDo(t, typed("text/html"), "GET", map[string]string{"Accept-Encoding": "identity"}).Header().Get("Content-Encoding") != "" {
		t.Error("client without gzip must not get gzip")
	}
}

func TestGzipKeepsContentTypeWhenHandlerReliedOnSniffing(t *testing.T) {
	rec := gzipDo(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<!DOCTYPE html><html><body>"+strings.Repeat("x", 500)+"</body></html>")
	}, "GET", nil)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("content-type = %q, encoding = %q", ct, rec.Header().Get("Content-Encoding"))
	}
}

func TestGzipSSEStillFlushes(t *testing.T) {
	rec := gzipDo(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		io.WriteString(w, "data: hi\n\n")
		w.(http.Flusher).Flush()
	}, "GET", nil)
	if rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != "data: hi\n\n" || !rec.Flushed {
		t.Fatalf("sse altered: %q flushed=%v enc=%q", rec.Body.String(), rec.Flushed, rec.Header().Get("Content-Encoding"))
	}
}
