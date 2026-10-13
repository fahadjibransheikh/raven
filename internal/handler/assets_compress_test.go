package handler

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testAssetCache() (*compressedAssetCache, string) {
	css := strings.Repeat(".panel { color: #123456; padding: 4px; }\n", 100)
	fs := fstest.MapFS{
		"css/app.css":  {Data: []byte(css)},
		"css/tiny.css": {Data: []byte("a{}")},
		"img/logo.png": {Data: []byte(strings.Repeat("x", 4096))},
	}
	return newCompressedAssetCache(http.FS(fs)), css
}

func serveTestAsset(cache *compressedAssetCache, name string, header http.Header) (*httptest.ResponseRecorder, bool) {
	req := httptest.NewRequest(http.MethodGet, "/assets/"+name, nil)
	for key, values := range header {
		req.Header[key] = values
	}
	rec := httptest.NewRecorder()
	return rec, cache.serve(rec, req, name)
}

func TestCompressedAssetServesGzipToBrowsersThatAcceptIt(t *testing.T) {
	cache, css := testAssetCache()
	rec, served := serveTestAsset(cache, "css/app.css", http.Header{"Accept-Encoding": {"gzip, deflate, br"}})
	if !served {
		t.Fatal("serve() = false, want the gzipped copy")
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("Vary = %q, want Accept-Encoding", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Fatalf("Content-Type = %q, want text/css", got)
	}
	if rec.Body.Len() >= len(css) {
		t.Fatalf("body is %d bytes, want less than the original %d", rec.Body.Len(), len(css))
	}
	reader, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("reading gzip body: %v", err)
	}
	if string(body) != css {
		t.Fatal("decompressed body differs from the original file")
	}
}

func TestCompressedAssetAnswersMatchingETagWithNotModified(t *testing.T) {
	cache, _ := testAssetCache()
	first, _ := serveTestAsset(cache, "css/app.css", http.Header{"Accept-Encoding": {"gzip"}})
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("first response has no ETag")
	}
	second, served := serveTestAsset(cache, "css/app.css", http.Header{"Accept-Encoding": {"gzip"}, "If-None-Match": {etag}})
	if !served || second.Code != http.StatusNotModified {
		t.Fatalf("served = %v, status = %d, want a 304", served, second.Code)
	}
}

func TestCompressedAssetLeavesOtherRequestsToTheFileServer(t *testing.T) {
	cache, _ := testAssetCache()
	for _, tc := range []struct {
		name   string
		asset  string
		header http.Header
	}{
		{"no Accept-Encoding", "css/app.css", nil},
		{"gzip refused", "css/app.css", http.Header{"Accept-Encoding": {"gzip;q=0, br"}}},
		{"range request", "css/app.css", http.Header{"Accept-Encoding": {"gzip"}, "Range": {"bytes=0-10"}}},
		{"tiny file", "css/tiny.css", http.Header{"Accept-Encoding": {"gzip"}}},
		{"image", "img/logo.png", http.Header{"Accept-Encoding": {"gzip"}}},
		{"missing file", "css/missing.css", http.Header{"Accept-Encoding": {"gzip"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, served := serveTestAsset(cache, tc.asset, tc.header)
			if served {
				t.Fatal("serve() = true, want the file server to handle it")
			}
			if got := rec.Header().Get("Content-Encoding"); got != "" {
				t.Fatalf("Content-Encoding = %q, want none", got)
			}
		})
	}
}

func TestAcceptsGzip(t *testing.T) {
	for header, want := range map[string]bool{
		"":                  false,
		"gzip":              true,
		"GZIP;q=0.5":        true,
		"br, gzip;q=0":      false,
		"*":                 true,
		"*;q=0":             false,
		"identity, *;q=0.1": true,
		"gzip;q=0, *":       false,
		"deflate, br":       false,
	} {
		if got := acceptsGzip(header); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}
