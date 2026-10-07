package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/auth"
	"github.com/cristianadrielbraun/gofer/internal/storage"
)

// In no-login mode the Security tab must render a pane (HTMX swaps
// #settings-content out of the response; a redirect left the page empty and
// broke every later tab click).
func TestSettingsTabsRenderContentWithoutLogin(t *testing.T) {
	db, err := storage.New(filepath.Join(t.TempDir(), "gofer.db"))
	if err != nil {
		t.Fatalf("storage.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h := &Handler{db: db}

	for _, tab := range []string{"security", "advanced"} {
		req := httptest.NewRequest(http.MethodGet, "/settings/"+tab, nil)
		req.SetPathValue("tab", tab)
		req.Header.Set("HX-Request", "true")
		req = req.WithContext(auth.ContextWithUser(context.Background(), &auth.User{ID: "default"}))
		rec := httptest.NewRecorder()
		h.handleSettingsTab(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, `id="settings-content"`) {
			t.Fatalf("%s: status %d, has settings-content %v", tab, rec.Code, strings.Contains(body, `id="settings-content"`))
		}
		if tab == "security" && !strings.Contains(body, "Login is off") {
			t.Fatalf("security tab missing explanation: %s", body)
		}
	}
}
