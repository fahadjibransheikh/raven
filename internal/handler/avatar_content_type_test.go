package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/storage"
)

// Avatars saved before their types were checked may carry the type a
// sender's server chose. They are refused instead of served as a page.
func TestAvatarsAreServedOnlyAsSandboxedImages(t *testing.T) {
	h := &Handler{}
	serve := func(contentType string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.serveAvatarImage(rec, httptest.NewRequest(http.MethodGet, "/api/avatars/0bc83cb571cd1c50ba6f3e8a78ef1346", nil), &storage.SenderAvatarRecord{
			EmailHash: "0bc83cb571cd1c50ba6f3e8a78ef1346", Status: "found", ContentType: contentType, ImageData: []byte("<script>alert(1)</script>"),
		})
		return rec
	}
	for _, poisoned := range []string{"image/png, text/html", "text/html", "image/svg+xml,text/html"} {
		if rec := serve(poisoned); rec.Code != http.StatusNotFound {
			t.Fatalf("avatar stored as %q served with %d %q", poisoned, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	rec := serve("image/png; charset=binary")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || !strings.HasSuffix(rec.Header().Get("Content-Security-Policy"), "; sandbox") || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("image avatar = %d type=%q csp=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("Content-Security-Policy"))
	}
}
