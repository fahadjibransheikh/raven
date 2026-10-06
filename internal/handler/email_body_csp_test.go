package handler

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func getEmailBody(t *testing.T, h *Handler, id int64, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/email/x/body"+query, nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	h.handleEmailBody(rec, ownerRequest(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
	return rec
}

func cspDirectives(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, v := range rec.Header().Values("Content-Security-Policy") {
		for _, part := range strings.Split(v, ";") {
			f := strings.Fields(part)
			if len(f) > 0 {
				out[f[0]] = strings.Join(f[1:], " ")
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no Content-Security-Policy header on email body")
	}
	return out
}

func TestEmailBodyCSPAllowsOnlyOurNoncedScripts(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fx := insertVictimReadableMessage(t, h, db)
	if err := db.SetUISettings(t.Context(), "owner", map[string]string{"load_remote_images": "false"}); err != nil {
		t.Fatal(err)
	}

	rec := getEmailBody(t, h, fx.messageID, "")
	d := cspDirectives(t, rec)
	if _, ok := d["sandbox"]; !ok || strings.Contains(d["sandbox"], "allow-same-origin") || strings.Contains(d["sandbox"], "allow-popups") {
		t.Errorf("sandbox directive = %q, want scripts only", d["sandbox"])
	}
	if d["default-src"] != "'none'" || d["form-action"] != "'none'" || d["base-uri"] != "'none'" {
		t.Errorf("default-src/form-action/base-uri must be 'none': %v", d)
	}
	m := regexp.MustCompile(`^'nonce-([A-Za-z0-9+/=_-]{16,})'$`).FindStringSubmatch(d["script-src"])
	if m == nil {
		t.Fatalf("script-src = %q, want a single nonce", d["script-src"])
	}
	if got := d["img-src"]; got != "'self' data:" {
		t.Errorf("img-src with remote blocked = %q, want 'self' data:", got)
	}
	body := rec.Body.String()
	if n := strings.Count(body, "<script"); n == 0 || n != strings.Count(body, `<script nonce="`+m[1]+`">`) {
		t.Errorf("every injected <script> must carry the nonce: %d scripts, body %q", n, body)
	}
	if rec2 := getEmailBody(t, h, fx.messageID, ""); cspDirectives(t, rec2)["script-src"] == d["script-src"] {
		t.Error("nonce is reused across responses")
	}

	if got := cspDirectives(t, getEmailBody(t, h, fx.messageID, "?remote=true"))["img-src"]; got != "'self' data: https: http:" {
		t.Errorf("img-src with remote allowed = %q", got)
	}
}

func TestEmailBodyLinksAreOpenedByTheParent(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fx := insertVictimReadableMessage(t, h, db)
	body := getEmailBody(t, h, fx.messageID, "").Body.String()
	if !strings.Contains(body, "emailLinkClick") || strings.Contains(body, "'_blank'") {
		t.Errorf("link script must hand clicks to the parent, not open popups itself: %q", body)
	}
	if !strings.Contains(body, "emailKeydown") {
		t.Errorf("frame must forward keystrokes so shortcuts survive focus in the message: %q", body)
	}
}
