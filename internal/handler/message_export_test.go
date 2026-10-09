package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestEMLFilename(t *testing.T) {
	long := strings.Repeat("é", 100)
	for in, want := range map[string]string{
		"Hello":                   "Hello.eml",
		"":                        "message.eml",
		"  \t\n ":                 "message.eml",
		`a/b\c"d` + "\x00\r\ne":   "a b c d e.eml",
		"../../etc/passwd":        "etc passwd.eml",
		"..":                      "message.eml",
		"Re: quarterly <report>?": "Re quarterly report.eml",
		long:                      strings.Repeat("é", 80) + ".eml",
	} {
		if got := emlFilename(in); got != want {
			t.Errorf("emlFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func messageExportRequest(h *Handler, handle http.HandlerFunc, id int64, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/messages/x", nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	handle(rec, as(req))
	return rec
}

func TestRawMessageDownload(t *testing.T) {
	f := newMessageActionOwnershipFixture(t)
	h := f.handler
	eml := "From: a@example.com\r\nSubject: Quarterly: plan\r\n\r\nbody\r\n"
	rawPath, err := h.blobStore.StoreRaw(t.Context(), "victim-account", f.victimMessageID, []byte(eml))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Write().ExecContext(t.Context(), `UPDATE messages SET raw_path = ?, subject = 'Quarterly: plan' WHERE id = ?`, rawPath, f.victimMessageID); err != nil {
		t.Fatal(err)
	}

	rec := messageExportRequest(h, h.handleMessageRaw, f.victimMessageID, ownerRequest)
	if rec.Code != http.StatusOK || rec.Body.String() != eml {
		t.Fatalf("owner: status %d body %q", rec.Code, rec.Body.String())
	}
	for k, want := range map[string]string{
		"Content-Type":           "message/rfc822",
		"Content-Disposition":    `attachment; filename="Quarterly plan.eml"`,
		"X-Content-Type-Options": "nosniff",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	if rec := messageExportRequest(h, h.handleMessageRaw, f.victimMessageID, func(r *http.Request) *http.Request { return attackerRequestWithAdmin(r, false) }); rec.Code != http.StatusNotFound {
		t.Errorf("foreign user: status %d, want 404", rec.Code)
	}

	// A stored path outside the blob directory is never served.
	outside := filepath.Join(t.TempDir(), "secret.eml")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Write().ExecContext(t.Context(), `UPDATE messages SET raw_path = ? WHERE id = ?`, outside, f.victimMessageID); err != nil {
		t.Fatal(err)
	}
	if rec := messageExportRequest(h, h.handleMessageRaw, f.victimMessageID, ownerRequest); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("outside path: status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestPrintMessagePage(t *testing.T) {
	f := newMessageActionOwnershipFixture(t)
	h := f.handler
	if _, err := h.db.Write().ExecContext(t.Context(), `UPDATE messages SET subject = '<b>Plan</b> & more', from_name = 'Eve <script>x</script>' WHERE id = ?`, f.victimMessageID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.bodyPath, []byte(`<p>victim body</p><script>alert(1)</script>`), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := messageExportRequest(h, h.handlePrintMessage, f.victimMessageID, ownerRequest)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner: status %d body %q", rec.Code, rec.Body.String())
	}
	csp := cspDirectives(t, rec)
	if s := csp["script-src"]; !strings.HasPrefix(s, "'nonce-") || strings.Contains(s, "unsafe-inline") || strings.Contains(s, " ") {
		t.Errorf("script-src = %q, want only a nonce", s)
	}
	for dir, want := range map[string]string{"default-src": "'none'", "base-uri": "'none'", "form-action": "'none'", "frame-ancestors": "'self'"} {
		if csp[dir] != want {
			t.Errorf("%s = %q, want %q", dir, csp[dir], want)
		}
	}
	if strings.Contains(csp["img-src"], "http") {
		t.Errorf("img-src %q allows remote images by default", csp["img-src"])
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	out := rec.Body.String()
	if !strings.Contains(out, "victim body") || !strings.Contains(out, "window.print()") {
		t.Errorf("page lacks body or print call:\n%s", out)
	}
	// Header fields are escaped, never raw markup.
	for _, bad := range []string{"<b>Plan</b>", "Eve <script>"} {
		if strings.Contains(out, bad) {
			t.Errorf("unescaped %q in page", bad)
		}
	}
	if !strings.Contains(out, "&lt;b&gt;Plan&lt;/b&gt; &amp; more") {
		t.Errorf("escaped subject missing:\n%s", out)
	}
	// Every script that survives (sanitizer miss included) is blocked by the CSP unless it carries our nonce.
	nonce := strings.TrimSuffix(strings.TrimPrefix(csp["script-src"], "'nonce-"), "'")
	for _, tag := range strings.Split(out, "<script")[1:] {
		if !strings.HasPrefix(tag, ` nonce="`+nonce+`"`) {
			t.Errorf("script tag without the response nonce: <script%.40s", tag)
		}
	}

	if rec := messageExportRequest(h, h.handlePrintMessage, f.victimMessageID, func(r *http.Request) *http.Request { return attackerRequestWithAdmin(r, false) }); rec.Code != http.StatusNotFound {
		t.Errorf("foreign user: status %d, want 404", rec.Code)
	}
}
