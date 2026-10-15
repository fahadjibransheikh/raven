package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/netguard"
)

func countingServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

func approveOnly(server *httptest.Server) func(context.Context, string, string, int) bool {
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	return func(_ context.Context, protocol, host string, p int) bool {
		return protocol == "http" && host == u.Hostname() && p == port
	}
}

// Remote image addresses come from the sender, so loading images must not
// reach the Raven server's own network unless an administrator allowed it.
func TestRemoteResourceDownloaderRefusesPrivateTargets(t *testing.T) {
	internal, internalHits := countingServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("internal data")) })

	if _, err := newRemoteResourceDownloader(nil)(t.Context(), internal.URL+"/admin"); !netguard.IsBlocked(err) {
		t.Fatalf("download error = %v, want a private target refusal", err)
	}
	if internalHits.Load() != 0 {
		t.Fatal("the private server was contacted")
	}

	data, err := newRemoteResourceDownloader(approveOnly(internal))(t.Context(), internal.URL+"/logo.png")
	if err != nil || string(data) != "internal data" {
		t.Fatalf("approved download = %q, %v", data, err)
	}

	redirector, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL+"/admin", http.StatusFound)
	})
	before := internalHits.Load()
	if _, err := newRemoteResourceDownloader(approveOnly(redirector))(t.Context(), redirector.URL+"/image.png"); !netguard.IsBlocked(err) {
		t.Fatalf("redirected download error = %v, want a private target refusal", err)
	}
	if internalHits.Load() != before {
		t.Fatal("a redirect reached an unapproved private server")
	}

	if _, err := newRemoteResourceDownloader(nil)(t.Context(), "file:///etc/passwd"); err == nil {
		t.Fatal("downloaded a file URL")
	}
}

// An administrator's private-target exception is for servers they named, such
// as a calendar host. A sender-chosen image URL must not ride on it.
func TestHandlerRemoteImagesIgnorePrivateTargetExceptions(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	internal, internalHits := countingServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("internal data")) })
	u, _ := url.Parse(internal.URL)
	port, _ := strconv.Atoi(u.Port())
	if err := db.AddPrivateTargetException(t.Context(), "http", u.Hostname(), port, "administrator"); err != nil {
		t.Fatal(err)
	}
	if !privateTargetAllowed(db)(t.Context(), "http", u.Hostname(), port) {
		t.Fatal("test setup: the exception is not in effect")
	}

	if _, err := h.downloadRemote(t.Context(), internal.URL+"/logo.png"); !netguard.IsBlocked(err) {
		t.Fatalf("default downloader error = %v, want a private target refusal", err)
	}
	h.remoteResourceDownloader = nil
	if _, err := h.downloadRemote(t.Context(), internal.URL+"/logo.png"); !netguard.IsBlocked(err) {
		t.Fatalf("fallback downloader error = %v, want a private target refusal", err)
	}
	if internalHits.Load() != 0 {
		t.Fatal("the approved private server was contacted for a sender-chosen URL")
	}
}
