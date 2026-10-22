package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/auth"
)

// A 5xx from Raven makes Cloudflare replace the body with its own HTML error
// page, which the settings toast then printed verbatim. A rejected test push
// is answered with a 4xx so the plain-text reason reaches the browser.
func TestTestPushFailureIsNotA5xx(t *testing.T) {
	h := &Handler{}
	h.SetTestPushSender(func(context.Context, string, string) error {
		return errors.New("the push service rejected the notification: 403 Forbidden")
	})
	req := httptest.NewRequest(http.MethodPost, "/api/push/test", strings.NewReader(`{"endpoint":"https://web.push.apple.com/x"}`))
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{ID: "user"}))
	rec := httptest.NewRecorder()
	h.handleTestPush(rec, req)
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("status = %d, want 4xx", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "403 Forbidden") {
		t.Fatalf("body = %q, want the push service's reason", rec.Body.String())
	}
}
