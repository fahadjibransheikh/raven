package netguard

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestForbiddenIP(t *testing.T) {
	forbidden := []string{
		"127.0.0.1", "127.1.2.3", "10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "100.64.0.1", "100.100.100.200", "100.127.255.255", "0.0.0.0", "0.1.2.3",
		"224.0.0.1", "239.255.255.255", "255.255.255.255", "240.0.0.1", "192.0.0.8", "198.18.0.1",
		"::", "::1", "fe80::1", "fc00::1", "fd12:3456::1", "ff02::1", "64:ff9b::7f00:1", "2002:7f00:1::1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254", "::127.0.0.1",
	}
	for _, s := range forbidden {
		if !ForbiddenIP(net.ParseIP(s)) {
			t.Errorf("%s should be forbidden", s)
		}
	}
	allowed := []string{"8.8.8.8", "93.184.216.34", "172.32.0.1", "100.63.255.255", "100.128.0.1", "2606:4700:4700::1111", "2a00:1450:4001::200e"}
	for _, s := range allowed {
		if ForbiddenIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
	if !ForbiddenIP(nil) {
		t.Error("nil IP must be forbidden")
	}
}

func TestFetchRefusesLoopbackServer(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer srv.Close()
	if _, err := Fetch(srv.URL, 1<<20); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("Fetch(loopback) error = %v, want refusal", err)
	}
	if hit {
		t.Fatal("request reached the loopback server")
	}
}

// A hostname that resolves to a forbidden address is refused at connect time,
// on the address actually dialled, so DNS rebinding cannot slip past a
// pre-check.
func TestFetchRefusesHostnameResolvingToLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	if _, err := Fetch("http://localhost:"+port+"/", 1<<20); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("Fetch(localhost) error = %v, want refusal", err)
	}
}

func TestFetchRejectsOtherSchemes(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "ftp://example.com/x", "gopher://example.com/", "javascript:1", "//example.com/x", ""} {
		if _, err := Fetch(u, 1<<20); err == nil {
			t.Errorf("Fetch(%q) should fail", u)
		}
	}
}

// testClient allows everything except the given port, standing in for "an
// internal address" so redirects can be exercised against local servers.
func testClient(blockedPort uint16) *http.Client {
	return newClient(5*time.Second, func(ap netip.AddrPort) bool { return ap.Port() == blockedPort })
}

func TestRedirectToForbiddenTargetIsRefused(t *testing.T) {
	var internalHit bool
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { internalHit = true }))
	defer internal.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer public.Close()
	internalPort := netip.MustParseAddrPort(internal.Listener.Addr().String()).Port()

	_, err := fetch(testClient(internalPort), public.URL, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("error = %v, want redirect target refused", err)
	}
	if internalHit {
		t.Fatal("redirect reached the forbidden target")
	}
}

func TestRedirectsAreCappedAndSchemeChecked(t *testing.T) {
	var hops int
	var loop *httptest.Server
	loop = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops++
		http.Redirect(w, r, loop.URL+fmt.Sprintf("/%d", hops), http.StatusFound)
	}))
	defer loop.Close()
	if _, err := fetch(testClient(0), loop.URL, 1<<20); err == nil || hops > maxRedirects+1 {
		t.Fatalf("error = %v after %d hops, want stop after %d redirects", err, hops, maxRedirects)
	}

	ftp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "ftp://example.com/x", http.StatusFound)
	}))
	defer ftp.Close()
	if _, err := fetch(testClient(0), ftp.URL, 1<<20); err == nil {
		t.Fatal("redirect to ftp:// should fail")
	}
}

func TestFetchCapsResponseSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 2048))
	}))
	defer srv.Close()
	if _, err := fetch(testClient(0), srv.URL, 1024); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error = %v, want too large", err)
	}
	data, err := fetch(testClient(0), srv.URL, 2048)
	if err != nil || len(data) != 2048 {
		t.Fatalf("at the cap: len=%d err=%v", len(data), err)
	}
}
