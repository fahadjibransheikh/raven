package netguard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestIsPrivate(t *testing.T) {
	for _, tc := range []struct {
		addr    string
		private bool
	}{
		{"8.8.8.8", false},
		{"2606:4700:4700::1111", false},
		{"64:ff9b::808:808", false},   // NAT64 for a public address
		{"2002:808:808::1", false},    // 6to4 for a public address
		{"::ffff:8.8.8.8", false},     // mapped public address
		{"127.0.0.1", true},           // loopback
		{"10.1.2.3", true},            // private
		{"172.16.0.1", true},          // private
		{"192.168.1.10", true},        // private
		{"169.254.169.254", true},     // cloud metadata, link-local
		{"100.64.0.1", true},          // carrier-grade NAT
		{"0.0.0.0", true},             // unspecified
		{"0.1.2.3", true},             // "this network"
		{"198.18.0.1", true},          // benchmarking
		{"203.0.113.5", true},         // documentation
		{"255.255.255.255", true},     // broadcast
		{"224.0.0.1", true},           // multicast
		{"::1", true},                 // loopback
		{"fd00::1", true},             // unique local
		{"fe80::1", true},             // link-local
		{"::ffff:127.0.0.1", true},    // mapped loopback
		{"64:ff9b::7f00:1", true},     // NAT64 for loopback
		{"64:ff9b::a00:1", true},      // NAT64 for 10.0.0.1
		{"2002:7f00:1::1", true},      // 6to4 for loopback
		{"2002:c0a8:10a::1", true},    // 6to4 for 192.168.1.10
		{"2001:0:4136:e378::1", true}, // Teredo
	} {
		if got := IsPrivate(netip.MustParseAddr(tc.addr)); got != tc.private {
			t.Errorf("IsPrivate(%s) = %v, want %v", tc.addr, got, tc.private)
		}
	}
	if !IsPrivate(netip.Addr{}) {
		t.Error("an invalid address must count as private")
	}
}

type fakeResolver struct {
	answers map[string][]netip.Addr
	calls   int
}

func (r *fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.calls++
	return r.answers[host], nil
}

func listen(t *testing.T) (port int, accepted chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted = make(chan struct{}, 4)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			_ = conn.Close()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port, accepted
}

func assertNoConnection(t *testing.T, accepted chan struct{}) {
	t.Helper()
	select {
	case <-accepted:
		t.Fatal("a refused target was still contacted")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDialRefusesPrivateTargets(t *testing.T) {
	port, accepted := listen(t)
	resolver := &fakeResolver{answers: map[string][]netip.Addr{
		"mail.internal.test": {netip.MustParseAddr("127.0.0.1")},
		"mixed.example.test": {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")},
	}}
	dialer := &Dialer{Dialer: &net.Dialer{Timeout: time.Second}, Resolver: resolver}
	for _, host := range []string{"mail.internal.test", "mixed.example.test", "127.0.0.1", "[::1]", "::ffff:127.0.0.1"} {
		_, err := dialer.Dial(t.Context(), "imap", host, port, false)
		if !IsBlocked(err) {
			t.Fatalf("Dial(%s) error = %v, want a private target refusal", host, err)
		}
	}
	assertNoConnection(t, accepted)
	if _, err := dialer.Dial(t.Context(), "imap", "fe80::1%eth0", port, false); err == nil || IsBlocked(err) {
		t.Fatalf("Dial with a zone error = %v, want a plain refusal", err)
	}
}

// An approved name connects to the address that was resolved once and checked,
// so a second DNS answer can't redirect the connection.
func TestDialConnectsToTheCheckedAddress(t *testing.T) {
	port, accepted := listen(t)
	resolver := &fakeResolver{answers: map[string][]netip.Addr{"mail.internal.test": {netip.MustParseAddr("127.0.0.1")}}}
	dialer := &Dialer{Dialer: &net.Dialer{Timeout: time.Second}, Resolver: resolver}
	conn, err := dialer.Dial(t.Context(), "imap", "mail.internal.test", port, true)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if got := conn.RemoteAddr().String(); got != net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) {
		t.Fatalf("connected to %s", got)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolved %d times, want once", resolver.calls)
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("approved target was not contacted")
	}
}

func TestBlockedErrorNamesTheSetting(t *testing.T) {
	err := &BlockedError{Protocol: "imap", Host: "192.168.1.10", Port: 993}
	want := "IMAP server 192.168.1.10:993 is on a private network; it needs a private target exception under Mail security"
	if err.Error() != want {
		t.Fatalf("Error() = %q", err.Error())
	}
}

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

// Dial refuses NAT64/6to4 addresses only when Strict, and Strict is what the
// HTTP transport uses; a plain Dialer (IMAP, SMTP) still allows a public
// address behind DNS64.
func TestStrictDialerRefusesTunnelAddresses(t *testing.T) {
	resolver := &fakeResolver{answers: map[string][]netip.Addr{
		"nat64.example.test": {netip.MustParseAddr("64:ff9b::808:808")},
		"6to4.example.test":  {netip.MustParseAddr("2002:808:808::1")},
	}}
	for _, host := range []string{"nat64.example.test", "6to4.example.test"} {
		strict := &Dialer{Dialer: &net.Dialer{Timeout: 50 * time.Millisecond}, Resolver: resolver, Strict: true}
		if _, err := strict.Dial(t.Context(), "https", host, 443, false); !IsBlocked(err) {
			t.Errorf("strict Dial(%s) error = %v, want a private target refusal", host, err)
		}
		if private, err := strict.IsPrivateHost(t.Context(), host); err != nil || !private {
			t.Errorf("strict IsPrivateHost(%s) = %v, %v", host, private, err)
		}
		lax := &Dialer{Dialer: &net.Dialer{Timeout: 50 * time.Millisecond}, Resolver: resolver}
		if _, err := lax.Dial(t.Context(), "imap", host, 993, false); IsBlocked(err) {
			t.Errorf("lax Dial(%s) was refused as private", host)
		}
	}
}
