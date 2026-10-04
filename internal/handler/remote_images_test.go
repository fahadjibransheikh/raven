package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestEmailBodyLoadsRemoteImagesUnlessSettingIsOff(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fixture := insertVictimReadableMessage(t, h, db)
	ctx := t.Context()

	// Stored bodies keep remote images blocked; the body handler decides
	// whether to restore them.
	var bodyPath string
	if err := db.Read().QueryRowContext(ctx, `SELECT body_html_path FROM messages WHERE id = ?`, fixture.messageID).Scan(&bodyPath); err != nil {
		t.Fatalf("body path: %v", err)
	}
	blocked := `<p>hi</p><img src="" data-remote-src="https://img.example/logo.png">`
	if err := os.WriteFile(bodyPath, []byte(blocked), 0o600); err != nil {
		t.Fatalf("write body: %v", err)
	}

	fetchBody := func() string {
		req := httptest.NewRequest(http.MethodGet, "/email/101/body", nil)
		req.SetPathValue("id", strconv.FormatInt(fixture.messageID, 10))
		rec := httptest.NewRecorder()
		h.handleEmailBody(rec, ownerRequest(req))
		if rec.Code != http.StatusOK {
			t.Fatalf("body status = %d", rec.Code)
		}
		return rec.Body.String()
	}

	if body := fetchBody(); !strings.Contains(body, `<img src="https://img.example/logo.png"`) {
		t.Fatalf("default settings body = %q, want remote image loaded", body)
	}

	if err := db.SetUISettings(ctx, "owner", map[string]string{"load_remote_images": "false"}); err != nil {
		t.Fatalf("SetUISettings: %v", err)
	}
	if body := fetchBody(); !strings.Contains(body, `data-remote-src="https://img.example/logo.png"`) || strings.Contains(body, `<img src="https://img.example/logo.png"`) {
		t.Fatalf("setting off body = %q, want remote image still blocked", body)
	}

	// Per-message approval still works when the setting is off.
	if err := db.AllowRemoteContentForMessageForUser(ctx, fixture.messageID, "owner"); err != nil {
		t.Fatalf("AllowRemoteContentForMessageForUser: %v", err)
	}
	if body := fetchBody(); !strings.Contains(body, `<img src="https://img.example/logo.png"`) {
		t.Fatalf("approved message body = %q, want remote image loaded", body)
	}
}
