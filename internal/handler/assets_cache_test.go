package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/views"
	"github.com/cristianadrielbraun/gofer/utils"
)

func TestAssetCachingVersionedImmutableUnversionedRevalidates(t *testing.T) {
	t.Setenv("GO_ENV", "production")
	t.Chdir("../..") // disk builds serve ./assets; a 404 would strip Cache-Control
	old := utils.ScriptVersion
	t.Cleanup(func() { utils.ScriptVersion = old })
	mux := http.NewServeMux()
	setupAssetsRoutes(mux)

	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}
	versioned := get("/assets/logo.svg?v=" + utils.ScriptVersion)
	if cc := versioned.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("versioned Cache-Control = %q, want immutable", cc)
	}
	if cc := get("/assets/logo.svg?v=stale").Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Fatalf("stale-version Cache-Control = %q, want no-cache", cc)
	}
}

func TestAssetURLChangesWithVersion(t *testing.T) {
	old := utils.ScriptVersion
	t.Cleanup(func() { utils.ScriptVersion = old })
	utils.ScriptVersion = "aaa"
	a := views.AssetURL("/assets/js/app.js")
	utils.ScriptVersion = "bbb"
	if b := views.AssetURL("/assets/js/app.js"); a == b || !strings.HasSuffix(a, "?v=aaa") {
		t.Fatalf("asset URL did not change with version: %q vs %q", a, b)
	}
}
