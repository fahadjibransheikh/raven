package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func setDAVPolicy(t *testing.T, fn func(context.Context, string, int) bool) {
	t.Helper()
	old := davPrivateAllowed
	davPrivateAllowed = fn
	t.Cleanup(func() { davPrivateAllowed = old })
}

func davGet(ctx context.Context, url string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := davHTTPClient(ctx, 5*time.Second).Do(req)
	if err == nil {
		resp.Body.Close()
	}
	return err
}

func TestDAVClientPrivateTargetPolicy(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	allow := func(context.Context, string, int) bool { return true }
	deny := func(context.Context, string, int) bool { return false }

	// Typed endpoint + policy allows (open/personal mode, or admin exception): works.
	setDAVPolicy(t, allow)
	if err := davGet(withDAVAnchor(context.Background(), srv.URL), srv.URL); err != nil {
		t.Fatalf("typed LAN endpoint refused: %v", err)
	}
	// Same host, different port is not the typed endpoint.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer other.Close()
	before := hits.Load()
	if err := davGet(withDAVAnchor(context.Background(), srv.URL), other.URL); err == nil || hits.Load() != before {
		t.Fatalf("non-anchor private target reached: err=%v", err)
	}
	// No anchor (URL learned from a server response): strict even when policy allows.
	if err := davGet(context.Background(), srv.URL); err == nil {
		t.Fatal("unanchored private target reached")
	}
	// Managed mode without an admin exception: strict even for the typed endpoint.
	setDAVPolicy(t, deny)
	if err := davGet(withDAVAnchor(context.Background(), srv.URL), srv.URL); err == nil {
		t.Fatal("typed endpoint reached despite deny policy")
	}
	// Nil policy is strict.
	setDAVPolicy(t, nil)
	if err := davGet(withDAVAnchor(context.Background(), srv.URL), srv.URL); err == nil {
		t.Fatal("typed endpoint reached with nil policy")
	}
}

func TestDAVClientRedirectToPrivateHostRefused(t *testing.T) {
	var internalHits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { internalHits.Add(1) }))
	defer internal.Close()
	// The "typed" server lives on localhost:PORT, redirects to the internal one
	// addressed by IP: a different host:port, so no exemption applies to the hop.
	typed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer typed.Close()
	setDAVPolicy(t, func(context.Context, string, int) bool { return true })
	if err := davGet(withDAVAnchor(context.Background(), typed.URL), typed.URL); err == nil || internalHits.Load() != 0 {
		t.Fatalf("redirect to internal host followed: err=%v hits=%d", err, internalHits.Load())
	}
}
