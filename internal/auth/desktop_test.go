package auth

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/storage"
)

const testDesktopToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newDesktopTestHandler(t *testing.T, cfg *Config) (*Manager, http.Handler, *int) {
	t.Helper()
	db, err := storage.New(filepath.Join(t.TempDir(), "gofer.db"))
	if err != nil {
		t.Fatalf("storage.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	manager := NewManager(cfg, db)
	if err := manager.EnsureDefaultUser(); err != nil {
		t.Fatalf("EnsureDefaultUser() error = %v", err)
	}
	calls := 0
	return manager, manager.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	})), &calls
}

func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func desktopCookie(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	rec := serve(h, httptest.NewRequest(http.MethodGet, DesktopAuthPath+"?t="+testDesktopToken, nil))
	for _, c := range rec.Result().Cookies() {
		if c.Name == desktopCookieName {
			return c
		}
	}
	t.Fatalf("no desktop cookie in response %d", rec.Code)
	return nil
}

func TestDesktopGateInactiveWithoutToken(t *testing.T) {
	_, h, calls := newDesktopTestHandler(t, &Config{})
	for _, path := range []string{"/", DesktopAuthPath} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Gofer-Request", "1")
		if rec := serve(h, req); rec.Code != http.StatusNoContent || rec.Header().Get(desktopMarkerKey) != "" {
			t.Fatalf("%s without token: status %d marker %q, want untouched passthrough", path, rec.Code, rec.Header().Get(desktopMarkerKey))
		}
	}
	if *calls != 2 {
		t.Fatalf("handler calls = %d, want 2", *calls)
	}
}

func TestDesktopGateRejectsRequestsWithoutCookie(t *testing.T) {
	_, h, calls := newDesktopTestHandler(t, &Config{DesktopToken: testDesktopToken})
	cases := map[string]*http.Request{
		"plain": httptest.NewRequest(http.MethodGet, "/", nil),
		"automation header": func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/folders/unread", nil)
			r.Header.Set("X-Gofer-Request", "1")
			return r
		}(),
		"sse":   httptest.NewRequest(http.MethodGet, "/api/events", nil),
		"setup": httptest.NewRequest(http.MethodGet, "/setup", nil),
		"wrong cookie": func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.AddCookie(&http.Cookie{Name: desktopCookieName, Value: "nope"})
			return r
		}(),
		"unsigned image route": httptest.NewRequest(http.MethodGet, "/api/inline-content/7/cid", nil),
	}
	for name, req := range cases {
		rec := serve(h, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, rec.Code)
		}
		if rec.Header().Get(desktopMarkerKey) != "1" {
			t.Errorf("%s: missing %s marker", name, desktopMarkerKey)
		}
	}
	if *calls != 0 {
		t.Fatalf("handler reached %d times without a cookie", *calls)
	}
}

func TestDesktopAuthSetsCookieAndRedirects(t *testing.T) {
	_, h, calls := newDesktopTestHandler(t, &Config{DesktopToken: testDesktopToken})

	rec := serve(h, httptest.NewRequest(http.MethodGet, DesktopAuthPath+"?t="+testDesktopToken, nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("auth = %d %q, want 303 /", rec.Code, rec.Header().Get("Location"))
	}
	cookie := desktopCookie(t, h)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("cookie attributes = %+v", cookie)
	}

	// The cookie opens the normal open-mode flow, including SSE and POSTs.
	for _, path := range []string{"/", "/api/events", "/auth/microsoft/mailbox/callback?code=x&state=y"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		if rec := serve(h, req); rec.Code != http.StatusNoContent {
			t.Fatalf("%s with cookie = %d, want 204", path, rec.Code)
		}
	}
	if *calls != 3 {
		t.Fatalf("handler calls = %d, want 3", *calls)
	}
}

func TestDesktopAuthRejectsBadTokenAndMethod(t *testing.T) {
	_, h, _ := newDesktopTestHandler(t, &Config{DesktopToken: testDesktopToken})
	for name, req := range map[string]*http.Request{
		"missing": httptest.NewRequest(http.MethodGet, DesktopAuthPath, nil),
		"wrong":   httptest.NewRequest(http.MethodGet, DesktopAuthPath+"?t=wrong", nil),
		"post":    httptest.NewRequest(http.MethodPost, DesktopAuthPath+"?t="+testDesktopToken, nil),
	} {
		rec := serve(h, req)
		if rec.Code != http.StatusForbidden || len(rec.Result().Cookies()) != 0 {
			t.Errorf("%s: status %d cookies %v, want 403 and no cookie", name, rec.Code, rec.Result().Cookies())
		}
	}
}

func TestDesktopAuthNextStaysSameOrigin(t *testing.T) {
	_, h, _ := newDesktopTestHandler(t, &Config{DesktopToken: testDesktopToken})
	for next, want := range map[string]string{
		"/?mailto=mailto%3Aa%40b.c": "/?mailto=mailto%3Aa%40b.c",
		"//evil.example/x":          "/",
		"/\\evil.example":           "/",
		"https://evil.example":      "/",
		"":                          "/",
	} {
		rec := serve(h, httptest.NewRequest(http.MethodGet, DesktopAuthPath+"?t="+testDesktopToken+"&next="+urlQueryEscape(next), nil))
		if got := rec.Header().Get("Location"); got != want {
			t.Errorf("next %q redirected to %q, want %q", next, got, want)
		}
	}
}

func urlQueryEscape(s string) string {
	return strings.NewReplacer("%", "%25", "&", "%26", "?", "%3F", "=", "%3D", ":", "%3A", "@", "%40", "\\", "%5C").Replace(s)
}

func TestDesktopImageGrantServesCookielessIframe(t *testing.T) {
	manager, h, calls := newDesktopTestHandler(t, &Config{DesktopToken: testDesktopToken})
	grant := manager.SignImageGrant(nil, 7)
	if grant == "" {
		t.Fatal("no desktop image grant issued")
	}
	ok := httptest.NewRequest(http.MethodGet, "/api/inline-content/7/cid?"+ImageGrantQuery+"="+grant, nil)
	if rec := serve(h, ok); rec.Code != http.StatusNoContent {
		t.Fatalf("granted image = %d, want 204", rec.Code)
	}
	for name, target := range map[string]string{
		"other message": "/api/inline-content/8/cid?" + ImageGrantQuery + "=" + grant,
		"other route":   "/api/folders/unread?" + ImageGrantQuery + "=" + grant,
		"tampered":      "/api/inline-content/7/cid?" + ImageGrantQuery + "=" + grant + "x",
	} {
		if rec := serve(h, httptest.NewRequest(http.MethodGet, target, nil)); rec.Code != http.StatusForbidden {
			t.Errorf("%s = %d, want 403", name, rec.Code)
		}
	}
	post := httptest.NewRequest(http.MethodPost, "/api/inline-content/7/cid?"+ImageGrantQuery+"="+grant, nil)
	if rec := serve(h, post); rec.Code != http.StatusForbidden {
		t.Errorf("POST with grant = %d, want 403", rec.Code)
	}
	if *calls != 1 {
		t.Fatalf("handler calls = %d, want 1", *calls)
	}
}

func TestDesktopImageGrantNotIssuedWithoutGate(t *testing.T) {
	manager, _, _ := newDesktopTestHandler(t, &Config{})
	if got := manager.SignImageGrant(nil, 7); got != "" {
		t.Fatalf("grant without desktop gate = %q, want empty", got)
	}
}

func TestDesktopTokenValidation(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg Config
		ok  bool
	}{
		"unset":        {Config{}, true},
		"valid":        {Config{DesktopToken: testDesktopToken}, true},
		"too short":    {Config{DesktopToken: "short"}, false},
		"auth enabled": {Config{DesktopToken: testDesktopToken, Enabled: true}, false},
	} {
		if err := tc.cfg.ValidateDesktopToken(); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", name, err, tc.ok)
		}
	}
}

func TestDesktopGateIgnoredWhenAuthEnabled(t *testing.T) {
	// Web/server deployments keep their own auth; a stray token must not change them.
	manager, _, _ := newDesktopTestHandler(t, &Config{Enabled: true, DesktopToken: testDesktopToken})
	if manager.desktopGateActive() {
		t.Fatal("desktop gate active in an authenticated mode")
	}
}
