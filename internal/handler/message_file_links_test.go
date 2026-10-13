package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/auth"
)

var signedMessageFileURL = regexp.MustCompile(`/api/message-file/[^"' ]+`)

func TestSignMessageFileLinksSignsOnlyThisMessagesFiles(t *testing.T) {
	h := &Handler{messageFileKey: newMessageFileKey()}
	body := `<img src="/api/inline-content/7/logo%40mail"><a href="/api/inline-content/7/invoice">x</a>` +
		`<div style="background:url(&#34;/api/remote-assets/7/0123456789abcdef.png&#34;)"></div>` +
		`<img src="/api/inline-content/8/other">`
	out := string(h.signMessageFileLinks(t.Context(), []byte(body), "owner", 7))
	if got := len(signedMessageFileURL.FindAllString(out, -1)); got != 3 {
		t.Fatalf("signed %d links, want 3: %s", got, out)
	}
	if !strings.Contains(out, `src="/api/inline-content/8/other"`) {
		t.Fatalf("signed another message's file: %s", out)
	}
	for _, want := range []string{"/api/message-file/inline/7/logo@mail?", "/api/message-file/remote/7/0123456789abcdef.png?"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
}

func signedRequest(t *testing.T, h *Handler, link string) *http.Request {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, messageFileRoute), "/")
	req := httptest.NewRequest(http.MethodGet, link, nil)
	req.SetPathValue("kind", parts[0])
	req.SetPathValue("messageID", parts[1])
	req.SetPathValue("name", parts[2])
	return req
}

func TestSignedMessageFileLinksRejectTampering(t *testing.T) {
	h := &Handler{messageFileKey: newMessageFileKey()}
	link := signedMessageFileURL.FindString(string(h.signMessageFileLinks(t.Context(), []byte(`<img src="/api/inline-content/7/logo">`), "owner", 7)))
	if _, _, ok := h.verifiedMessageFileRequest(signedRequest(t, h, link)); !ok {
		t.Fatalf("a valid link was refused: %s", link)
	}
	u, _ := url.Parse(link)
	exp := u.Query().Get("e")
	tamper := map[string]string{
		"owner":   strings.Replace(link, "u=owner", "u=attacker", 1),
		"message": strings.Replace(link, "/inline/7/", "/inline/8/", 1),
		"file":    strings.Replace(link, "/logo?", "/other?", 1),
		"kind":    strings.Replace(link, "/inline/", "/remote/", 1),
		"expiry":  strings.Replace(link, "e="+exp, "e=9999999999", 1),
		"sig":     link[:len(link)-1] + "0",
	}
	for name, bad := range tamper {
		if _, _, ok := h.verifiedMessageFileRequest(signedRequest(t, h, bad)); ok {
			t.Fatalf("tampered %s was accepted: %s", name, bad)
		}
	}
	web := h.remoteContentLinks(t.Context(), "owner", 7)("https://images.example/logo.png?size=2")
	if _, _, ok := h.verifiedMessageFileRequest(signedRequest(t, h, web)); !ok {
		t.Fatalf("a valid relay link was refused: %s", web)
	}
	for name, bad := range map[string]string{
		"address":       strings.Replace(web, "images.example", "attacker.example", 1),
		"relay kind":    strings.Replace(web, "/web/", "/remote/", 1),
		"relay owner":   strings.Replace(web, "u=owner", "u=attacker", 1),
		"relay message": strings.Replace(web, "/web/7/", "/web/8/", 1),
	} {
		if _, _, ok := h.verifiedMessageFileRequest(signedRequest(t, h, bad)); ok {
			t.Fatalf("tampered %s was accepted: %s", name, bad)
		}
	}
	other := &Handler{messageFileKey: newMessageFileKey()}
	if _, _, ok := other.verifiedMessageFileRequest(signedRequest(t, other, link)); ok {
		t.Fatal("a link from another server start was accepted")
	}
	expired := time.Now().Add(-time.Minute).Unix()
	q := url.Values{"u": {"owner"}, "e": {strconv.FormatInt(expired, 10)}, "s": {h.messageFileSignature(messageFileInline, "owner", "", 7, "logo", expired)}}
	if _, _, ok := h.verifiedMessageFileRequest(signedRequest(t, h, messageFileRoute+"inline/7/logo?"+q.Encode())); ok {
		t.Fatal("an expired link was accepted")
	}
}

// The message frame counts as another site, so its image requests carry no
// session cookie. A signed link serves the file anyway, and nothing else
// does.
func TestSignedMessageFileServesWithoutASession(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fixture := insertVictimReadableMessage(t, h, db)
	manager := auth.NewManager(&auth.Config{Enabled: true, Mode: auth.ModeManaged, BaseURL: "https://gofer.example"}, db)
	h.auth = manager
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	stack := manager.Middleware(mux)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		stack.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	plain := "/api/inline-content/" + strconv.FormatInt(fixture.messageID, 10) + "/" + fixture.contentID
	if rec := get(plain); rec.Code == http.StatusOK {
		t.Fatal("the unsigned address served a file without a session")
	}
	session, err := manager.CreateSession(t.Context(), "owner", "test browser")
	if err != nil {
		t.Fatal(err)
	}
	signedIn := auth.ContextWithSession(t.Context(), session)
	link := signedMessageFileURL.FindString(string(h.signMessageFileLinks(signedIn, []byte(`<img src="`+plain+`">`), "owner", fixture.messageID)))
	rec := get(link)
	if rec.Code != http.StatusOK || rec.Header().Get("Cross-Origin-Resource-Policy") != "cross-origin" || !strings.HasSuffix(rec.Header().Get("Content-Security-Policy"), "; sandbox") {
		t.Fatalf("signed link = %d CORP=%q CSP=%q", rec.Code, rec.Header().Get("Cross-Origin-Resource-Policy"), rec.Header().Get("Content-Security-Policy"))
	}
	attacker := signedMessageFileURL.FindString(string(h.signMessageFileLinks(signedIn, []byte(`<img src="`+plain+`">`), "attacker", fixture.messageID)))
	if rec := get(attacker); rec.Code == http.StatusOK {
		t.Fatal("a link signed for another person served the owner's file")
	}
	// Where people sign in, a link made without a session is refused.
	unsigned := signedMessageFileURL.FindString(string(h.signMessageFileLinks(t.Context(), []byte(`<img src="`+plain+`">`), "owner", fixture.messageID)))
	if rec := get(unsigned); rec.Code == http.StatusOK {
		t.Fatal("a link made without a session served a file")
	}
	// Signing out ends the links made for that session.
	if _, err := manager.RevokeSession(t.Context(), "owner", session.ID, "owner", auth.SessionRevocationLogout); err != nil {
		t.Fatal(err)
	}
	if rec := get(link); rec.Code == http.StatusOK {
		t.Fatal("a link still served a file after its session signed out")
	}
}
