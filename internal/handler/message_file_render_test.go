package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/auth"
)

func TestEmailBodyGetsSignedImageLinksAtRenderTimeOnly(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fx := insertVictimReadableMessage(t, h, db)
	h.auth = auth.NewManager(&auth.Config{Enabled: true}, db, auth.Dependencies{BucketHashKey: []byte("0123456789abcdef0123456789abcdef")})

	id := strconv.FormatInt(fx.messageID, 10)
	stored := `<p><img src="/api/inline-content/` + id + `/victim-inline"><img src="/api/remote-assets/` + id + `/` + fx.assetName +
		`"><img src="/api/inline-content/999/other"></p>`
	var bodyPath string
	if err := db.Read().QueryRowContext(t.Context(), `SELECT body_html_path FROM messages WHERE id = ?`, fx.messageID).Scan(&bodyPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bodyPath, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/email/x/body", nil)
	req.SetPathValue("id", id)
	req = ownerRequest(req)
	req = req.WithContext(auth.ContextWithSession(req.Context(), &auth.Session{ID: "sess", UserID: "owner"}))
	rec := httptest.NewRecorder()
	h.handleEmailBody(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	out := rec.Body.String()
	for _, want := range []string{"/api/message-file/inline/" + id + "/victim-inline?", "/api/message-file/remote/" + id + "/" + fx.assetName + "?"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered body missing signed link %q", want)
		}
	}
	if strings.Contains(out, "/api/message-file/inline/999/") || !strings.Contains(out, "/api/inline-content/999/other") {
		t.Error("another message's image URL was signed")
	}
	if got, _ := os.ReadFile(bodyPath); string(got) != stored || strings.Contains(string(got), "message-file") {
		t.Errorf("stored body changed: %q", got)
	}
}
