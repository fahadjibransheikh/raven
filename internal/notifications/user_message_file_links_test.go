package notifications

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var signedMessageFileLink = regexp.MustCompile(`/api/message-file/[^"' )]+`)

// The message frame counts as another site, so the images in it are fetched
// without a session. The page signs them for their owner, and only that
// owner's signature serves the file.
func TestOwnedMessagePageSignsItsImagesForUseWithoutASession(t *testing.T) {
	f := newUserStorageFixtureMode(t, true)
	server := newRoutedIMAPServer(t)
	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("remote image")) }))
	t.Cleanup(image.Close)
	allowPrivateImageServer(t, f, image)
	server.mu.Lock()
	server.bodyOverride = "From: sender@example.com\r\nSubject: Logo\r\nContent-Type: text/html\r\n\r\n<p>hello</p><img src=\"" + image.URL + "/logo.png\">"
	server.mu.Unlock()
	f.useIMAPServer(t, server)
	if err := f.imap.Sync(t.Context(), "alice", f.accounts["alice"].ID); err != nil {
		t.Fatal(err)
	}
	if r := f.request("alice", "POST", "/api/remote-content/1/allow", `{"mode":"email"}`); r.Code != 200 {
		t.Fatal("allow remote content", r.Code, r.Body.String())
	}
	page := f.request("alice", "GET", "/email/1/body", "")
	link := strings.ReplaceAll(signedMessageFileLink.FindString(page.Body.String()), "&amp;", "&")
	if page.Code != 200 || !strings.HasPrefix(link, "/api/message-file/remote/1/") || strings.Contains(page.Body.String(), `src="/api/remote-assets/`) {
		t.Fatalf("message page did not sign its image: %d %s", page.Code, page.Body.String())
	}

	signed := f.request("", "GET", link, "")
	if signed.Code != 200 || signed.Body.String() != "remote image" || signed.Header().Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Fatalf("signed image without a session = %d %q CORP=%q", signed.Code, signed.Body.String(), signed.Header().Get("Cross-Origin-Resource-Policy"))
	}
	if r := f.request("", "GET", strings.Replace(link, "u=alice", "u=bob", 1), ""); r.Code == 200 {
		t.Fatal("a link altered to name another person served a file")
	}
	unsigned := strings.Replace(strings.SplitN(link, "?", 2)[0], "/api/message-file/remote/", "/api/remote-assets/", 1)
	if r := f.request("", "GET", unsigned, ""); r.Code == 200 {
		t.Fatal("the unsigned address served a file without a session")
	}
}

// Loading remote content once sends it through Gofer like the saved modes, so
// the page never contacts the sender's server and the browser has no
// third-party tracker to block.
func TestLoadOnceRelaysRemoteContentThroughGofer(t *testing.T) {
	f := newUserStorageFixtureMode(t, true)
	server := newRoutedIMAPServer(t)
	png := "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"
	var fetched int
	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetched++
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(png))
	}))
	t.Cleanup(image.Close)
	allowPrivateImageServer(t, f, image)
	server.mu.Lock()
	server.bodyOverride = "From: sender@example.com\r\nSubject: Logo\r\nContent-Type: text/html\r\n\r\n<p>hello</p><img src=\"" + image.URL + "/logo.png\"><div style=\"background:url(" + image.URL + "/bg.png)\">x</div>"
	server.mu.Unlock()
	f.useIMAPServer(t, server)
	if err := f.imap.Sync(t.Context(), "alice", f.accounts["alice"].ID); err != nil {
		t.Fatal(err)
	}
	if r := f.request("alice", "POST", "/api/remote-content/1/allow", `{"mode":"once"}`); r.Code != 200 {
		t.Fatal("load once", r.Code, r.Body.String())
	}
	page := f.request("alice", "GET", "/email/1/body?remote=true", "")
	body := page.Body.String()
	links := signedMessageFileLink.FindAllString(body, -1)
	if page.Code != 200 || strings.Contains(body, image.URL) || len(links) != 2 {
		t.Fatalf("page did not relay its remote content: %d %s", page.Code, body)
	}
	if csp := page.Header().Get("Content-Security-Policy"); strings.Contains(csp, "http") {
		t.Fatalf("page may still load from the web: %s", csp)
	}
	for _, link := range links {
		link = strings.ReplaceAll(link, "&amp;", "&")
		if !strings.HasPrefix(link, "/api/message-file/web/1/") {
			t.Fatalf("unexpected link %s", link)
		}
		relayed := f.request("", "GET", link, "")
		if relayed.Code != 200 || relayed.Body.String() != png || relayed.Header().Get("Content-Type") != "image/png" || relayed.Header().Get("Cross-Origin-Resource-Policy") != "cross-origin" {
			t.Fatalf("relayed image = %d %q type=%q CORP=%q", relayed.Code, relayed.Body.String(), relayed.Header().Get("Content-Type"), relayed.Header().Get("Cross-Origin-Resource-Policy"))
		}
		if r := f.request("", "GET", strings.Replace(link, "u=alice", "u=bob", 1), ""); r.Code == 200 {
			t.Fatal("a link altered to name another person was relayed")
		}
	}
	if fetched != 2 {
		t.Fatalf("remote server was asked %d times, want 2", fetched)
	}
	if r := f.request("alice", "GET", "/email/1/body", ""); strings.Contains(r.Body.String(), "/api/message-file/web/") {
		t.Fatal("loading once was remembered")
	}
}
