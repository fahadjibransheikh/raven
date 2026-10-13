package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	mailpkg "github.com/fahadjibransheikh/raven/internal/mail"
	"github.com/fahadjibransheikh/raven/internal/storage"
)

func newPushHookTestHandler(t *testing.T, secret string) *Handler {
	t.Helper()
	db, err := storage.New(filepath.Join(t.TempDir(), "raven.db"))
	if err != nil {
		t.Fatalf("storage.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	syncer := mailpkg.NewSyncOrchestrator(db, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	syncer.StartPush(ctx, mailpkg.PushConfig{Secret: secret})
	return &Handler{db: db, syncer: syncer}
}

func servePushHook(h *Handler, target, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /hooks/gmail", h.handleGmailPushHook)
	mux.HandleFunc("POST /hooks/graph", h.handleGraphPushHook)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, target, strings.NewReader(body)))
	return recorder
}

func TestPushHooksAreNotFoundWhenPushIsDisabled(t *testing.T) {
	h := newPushHookTestHandler(t, "")
	for _, target := range []string{"/hooks/gmail?token=", "/hooks/graph?validationToken=abc"} {
		if got := servePushHook(h, target, "{}").Code; got != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", target, got)
		}
	}
}

func TestGmailPushHookChecksToken(t *testing.T) {
	h := newPushHookTestHandler(t, "s3cret")
	body := `{"message":{"data":"bm90LWpzb24="},"subscription":"s"}`
	for target, want := range map[string]int{
		"/hooks/gmail":                  http.StatusForbidden,
		"/hooks/gmail?token=":           http.StatusForbidden,
		"/hooks/gmail?token=wrong":      http.StatusForbidden,
		"/hooks/gmail?token=s3cret":     http.StatusNoContent,
		"/hooks/gmail?token=s3cret&x=1": http.StatusNoContent,
	} {
		if got := servePushHook(h, target, body).Code; got != want {
			t.Fatalf("%s status = %d, want %d", target, got, want)
		}
	}
	// Undecodable bodies still get 204 so Pub/Sub does not redeliver forever.
	if got := servePushHook(h, "/hooks/gmail?token=s3cret", "garbage").Code; got != http.StatusNoContent {
		t.Fatalf("garbage body status = %d, want 204", got)
	}
}

func TestGraphPushHookEchoesValidationToken(t *testing.T) {
	h := newPushHookTestHandler(t, "s3cret")
	token := "Validation: Testing client application reachability <b>&</b> for subscription Request-Id: abc"
	recorder := servePushHook(h, "/hooks/graph?account=acc&validationToken="+url.QueryEscape(token), "")
	if recorder.Code != http.StatusOK || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/plain") || recorder.Body.String() != token {
		t.Fatalf("validation reply = %d %q %q, want 200 text/plain %q", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String(), token)
	}
	long := strings.Repeat("a", 1025)
	if got := servePushHook(h, "/hooks/graph?validationToken="+long, "").Code; got != http.StatusBadRequest {
		t.Fatalf("oversized validation token status = %d, want 400", got)
	}
}

func TestGraphPushHookChecksClientState(t *testing.T) {
	h := newPushHookTestHandler(t, "s3cret")
	tests := []struct {
		name string
		body string
		want int
	}{
		{"valid", `{"value":[{"subscriptionId":"1","clientState":"s3cret"}]}`, http.StatusAccepted},
		{"valid lifecycle", `{"value":[{"clientState":"s3cret","lifecycleEvent":"reauthorizationRequired"}]}`, http.StatusAccepted},
		{"wrong", `{"value":[{"clientState":"nope"}]}`, http.StatusForbidden},
		{"missing", `{"value":[{"subscriptionId":"1"}]}`, http.StatusForbidden},
		{"one bad item", `{"value":[{"clientState":"s3cret"},{"clientState":"nope"}]}`, http.StatusForbidden},
		{"empty list", `{"value":[]}`, http.StatusBadRequest},
		{"not json", `nope`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := servePushHook(h, "/hooks/graph?account=missing", tt.body).Code; got != tt.want {
				t.Fatalf("status = %d, want %d", got, tt.want)
			}
		})
	}
}
