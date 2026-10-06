package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestEmailBodyBlocksRemoteImagesUnlessSettingIsOnOrAllowed(t *testing.T) {
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

	// New users have no stored choice: remote content is blocked and the banner
	// script reports it so "Show content" / "Always allow" are offered.
	if body := fetchBody(); !strings.Contains(body, `data-remote-src="https://img.example/logo.png"`) || strings.Contains(body, `<img src="https://img.example/logo.png"`) || !strings.Contains(body, "remoteContentBlocked") {
		t.Fatalf("default settings body = %q, want remote image blocked with banner script", body)
	}

	if err := db.SetUISettings(ctx, "owner", map[string]string{"load_remote_images": "true"}); err != nil {
		t.Fatalf("SetUISettings: %v", err)
	}
	if body := fetchBody(); !strings.Contains(body, `<img src="https://img.example/logo.png"`) {
		t.Fatalf("setting on body = %q, want remote image loaded", body)
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

func TestEmailBodyRestoresRemoteBackgroundsOnlyWhenAllowed(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fixture := insertVictimReadableMessage(t, h, db)
	ctx := t.Context()
	var bodyPath string
	if err := db.Read().QueryRowContext(ctx, `SELECT body_html_path FROM messages WHERE id = ?`, fixture.messageID).Scan(&bodyPath); err != nil {
		t.Fatalf("body path: %v", err)
	}
	blocked := `<style>.hero{background:url("raven-remote:https://img.example/hero.png")}</style><table><tr><td data-remote-bg="https://img.example/bg.png">x</td></tr></table>`
	if err := os.WriteFile(bodyPath, []byte(blocked), 0o600); err != nil {
		t.Fatalf("write body: %v", err)
	}
	fetch := func() (string, string) {
		req := httptest.NewRequest(http.MethodGet, "/email/101/body", nil)
		req.SetPathValue("id", strconv.FormatInt(fixture.messageID, 10))
		rec := httptest.NewRecorder()
		h.handleEmailBody(rec, ownerRequest(req))
		return rec.Body.String(), strings.Join(rec.Header().Values("Content-Security-Policy"), " | ")
	}

	if err := db.SetUISettings(ctx, "owner", map[string]string{"load_remote_images": "true"}); err != nil {
		t.Fatalf("SetUISettings: %v", err)
	}
	body, csp := fetch()
	if !strings.Contains(body, `url("https://img.example/hero.png")`) || !strings.Contains(body, `background="https://img.example/bg.png"`) {
		t.Errorf("allowed: backgrounds not restored: %s", body)
	}
	if !strings.Contains(csp, "img-src 'self' data: https: http:") || strings.Contains(csp, "font-src") {
		t.Errorf("allowed: CSP = %s, want remote images but no font-src", csp)
	}

	if err := db.SetUISettings(ctx, "owner", map[string]string{"load_remote_images": "false"}); err != nil {
		t.Fatalf("SetUISettings: %v", err)
	}
	body, csp = fetch()
	if strings.Contains(body, `url("https://img.example`) || strings.Contains(body, `background="https://img.example`) || !strings.Contains(body, `raven-remote:https://img.example/hero.png`) {
		t.Errorf("blocked: backgrounds restored or marker lost: %s", body)
	}
	if !strings.Contains(csp, "img-src 'self' data:;") || !strings.Contains(body, "data-remote-bg") {
		t.Errorf("blocked: CSP = %s", csp)
	}
	if !strings.Contains(body, "remoteContentBlocked") {
		t.Errorf("blocked: banner script missing")
	}
}

func TestAllowRemoteContentFromSenderWorksWithBlockedDefault(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fixture := insertVictimReadableMessage(t, h, db)
	ctx := t.Context()
	var bodyPath string
	if err := db.Read().QueryRowContext(ctx, `SELECT body_html_path FROM messages WHERE id = ?`, fixture.messageID).Scan(&bodyPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bodyPath, []byte(`<img src="" data-remote-src="https://img.example/logo.png">`), 0o600); err != nil {
		t.Fatal(err)
	}
	fetch := func(query string) string {
		req := httptest.NewRequest(http.MethodGet, "/email/101/body"+query, nil)
		req.SetPathValue("id", strconv.FormatInt(fixture.messageID, 10))
		rec := httptest.NewRecorder()
		h.handleEmailBody(rec, ownerRequest(req))
		return rec.Body.String()
	}
	loaded := `<img src="https://img.example/logo.png"`

	if body := fetch(""); strings.Contains(body, loaded) {
		t.Fatalf("default: remote image loaded, want blocked")
	}
	// "Show content" reloads the frame with remote=true.
	if body := fetch("?remote=true"); !strings.Contains(body, loaded) {
		t.Fatalf("remote=true: want loaded")
	}
	// "Always allow from sender" persists, so later opens load without the query.
	if err := db.AllowRemoteContentForSenderFromMessageForUser(ctx, fixture.messageID, "owner"); err != nil {
		t.Fatal(err)
	}
	if body := fetch(""); !strings.Contains(body, loaded) {
		t.Fatalf("after allowing sender: want loaded")
	}
}
