package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/auth"
	"github.com/fahadjibransheikh/raven/internal/calendar"
	"github.com/fahadjibransheikh/raven/internal/config"
	"github.com/fahadjibransheikh/raven/internal/netguard"
	"github.com/fahadjibransheikh/raven/internal/storage"
)

// davModes are the authentication modes and whether a typed DAV server on a
// private address works there without an administrator's exception. A handler
// with no auth manager counts as non-managed, as before the upstream merge.
var davModes = []struct {
	name    string
	auth    auth.Mode // "" builds a handler with no auth manager
	typedOK bool
}{
	{"no auth manager", "", true},
	{"open", auth.ModeOpen, true},
	{"personal", auth.ModePersonal, true},
	{"managed", auth.ModeManaged, false},
}

// useDAVMode points the DAV policy at a handler running in mode, with admin
// exceptions approving only what admin approves (nil approves nothing).
func useDAVMode(t *testing.T, mode auth.Mode, admin netguard.PrivatePolicy) {
	t.Helper()
	h := &Handler{}
	if mode != "" {
		db, err := storage.New(t.TempDir() + "/dav-mode.db")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		h.auth = auth.NewManager(&auth.Config{Mode: mode, BaseURL: "https://gofer.example"}, db)
	}
	previousPolicy, previousMode := davPrivateTargetPolicy.Load(), davOperatorOwnsNetwork.Load()
	t.Cleanup(func() { davPrivateTargetPolicy.Store(previousPolicy); davOperatorOwnsNetwork.Store(previousMode) })
	setDAVPrivateTargetPolicy(admin)
	setDAVOperatorOwnsNetwork(h.singleOperator)
}

func adminApproves(server *httptest.Server) netguard.PrivatePolicy {
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	return func(_ context.Context, protocol, host string, p int) bool {
		return protocol == "http" && host == u.Hostname() && p == port
	}
}

func davCalDAVGet(ctx context.Context, target string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	resp, err := (&http.Client{Transport: davHTTPTransport, Timeout: 5 * time.Second}).Do(req)
	if err == nil {
		resp.Body.Close()
	}
	return err
}

// Both transports go through the real netguard dialer here, against loopback
// servers (a private address). The same four rules must hold for each.
func TestDAVPrivateAnchorByMode(t *testing.T) {
	var hits atomic.Int32
	newServer := func() *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
		t.Cleanup(s.Close)
		return s
	}
	typed, other := newServer(), newServer()
	protocols := []struct {
		name string
		get  func(context.Context, string) error
	}{{"CardDAV", davGet}, {"CalDAV", davCalDAVGet}}

	for _, mode := range davModes {
		for _, protocol := range protocols {
			t.Run(mode.name+"/"+protocol.name, func(t *testing.T) {
				useDAVMode(t, mode.auth, nil)
				anchored := withDAVAnchor(context.Background(), typed.URL)

				// The typed or saved server itself.
				before := hits.Load()
				err := protocol.get(anchored, typed.URL)
				if mode.typedOK && (err != nil || hits.Load() != before+1) {
					t.Fatalf("typed LAN server refused in %s mode: %v", mode.name, err)
				}
				if !mode.typedOK && (err == nil || hits.Load() != before) {
					t.Fatalf("typed LAN server reached in managed mode without an exception: err=%v", err)
				}

				// A private host that only a server response named (an href or a
				// redirect target) is never the anchor.
				before = hits.Load()
				if err := protocol.get(anchored, other.URL); err == nil || hits.Load() != before {
					t.Fatalf("non-anchor private host reached: err=%v", err)
				}
				// No anchor at all (a host learned from a server response) is refused too.
				if err := protocol.get(context.Background(), typed.URL); err == nil || hits.Load() != before {
					t.Fatalf("private host reached with no anchor: err=%v", err)
				}
			})
		}
	}
}

// An administrator's private-target exception works in every mode, anchored or
// not for CalDAV (as before), and for the anchor in CardDAV.
func TestDAVAdminExceptionWorksInEveryMode(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer server.Close()
	for _, mode := range davModes {
		t.Run(mode.name, func(t *testing.T) {
			useDAVMode(t, mode.auth, adminApproves(server))
			before := hits.Load()
			if err := davGet(withDAVAnchor(context.Background(), server.URL), server.URL); err != nil {
				t.Fatalf("CardDAV anchor with exception: %v", err)
			}
			if err := davCalDAVGet(withDAVAnchor(context.Background(), server.URL), server.URL); err != nil {
				t.Fatalf("CalDAV anchor with exception: %v", err)
			}
			if err := davCalDAVGet(context.Background(), server.URL); err != nil {
				t.Fatalf("CalDAV host with exception but no anchor: %v", err)
			}
			if hits.Load() != before+3 {
				t.Fatalf("hits = %d, want %d", hits.Load()-before, 3)
			}
		})
	}
}

// A connection opened for the anchor must not carry a later request that has no
// approval: the CalDAV transport keeps no connection between requests.
func TestDAVCalDAVApprovalDoesNotOutliveTheRequest(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); _, _ = w.Write([]byte("ok")) }))
	defer server.Close()
	useDAVMode(t, auth.ModePersonal, nil)
	if err := davCalDAVGet(withDAVAnchor(context.Background(), server.URL), server.URL); err != nil {
		t.Fatal(err)
	}
	if err := davCalDAVGet(context.Background(), server.URL); err == nil || hits.Load() != 1 {
		t.Fatalf("unanchored request reused the anchored connection: err=%v hits=%d", err, hits.Load())
	}
}

// anchorProbe asks the CalDAV policy about each request exactly as the dialer
// would (same context, same host:port), then lets the request through. It
// proves each handler entry point carries the saved server as the anchor.
type anchorProbe struct {
	next            http.RoundTripper
	mu              sync.Mutex
	allowed, denied int
}

func (p *anchorProbe) RoundTrip(req *http.Request) (*http.Response, error) {
	host, portText, _ := net.SplitHostPort(req.URL.Host)
	port, _ := strconv.Atoi(portText)
	ok := davPrivateTargetAllowed(req.Context(), "https", host, port)
	p.mu.Lock()
	if ok {
		p.allowed++
	} else {
		p.denied++
	}
	p.mu.Unlock()
	return p.next.RoundTrip(req)
}

func (p *anchorProbe) take() (allowed, denied int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	allowed, denied = p.allowed, p.denied
	p.allowed, p.denied = 0, 0
	return
}

func TestCalDAVEntryPointsCarryTheSavedServerAsAnchor(t *testing.T) {
	for _, mode := range davModes {
		t.Run(mode.name, func(t *testing.T) {
			useDAVMode(t, mode.auth, nil)
			h := calendarCreateFixture(t)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			}))
			t.Cleanup(server.Close)
			probe := &anchorProbe{next: server.Client().Transport}
			previous := davHTTPTransport
			davHTTPTransport = probe
			t.Cleanup(func() { davHTTPTransport = previous })

			remote := server.URL + "/cal/"
			if _, err := h.db.Write().Exec(`UPDATE accounts SET provider='imap' WHERE id='one-account'; UPDATE calendar_sources SET provider='caldav',access_role='owner',remote_id=? WHERE id='one-source'`, remote); err != nil {
				t.Fatal(err)
			}
			var err error
			if h.accountStore, err = config.NewAccountStore(h.db, []byte("0123456789abcdef0123456789abcdef")); err != nil {
				t.Fatal(err)
			}
			if err := h.accountStore.SaveCalDAVConfig(t.Context(), "one", "one-account", remote, "user", "pass", false); err != nil {
				t.Fatal(err)
			}
			source := storage.CalendarSource{ID: "one-source", UserID: "one", AccountID: "one-account", Provider: "caldav", RemoteID: remote, AccessRole: "owner", IsSelected: true}
			start, end := time.Now().Add(time.Hour).UTC(), time.Now().Add(2*time.Hour).UTC()
			event := storage.CalendarEvent{ID: "event", UserID: "one", SourceID: "one-source", RemoteID: remote + "event.ics", ETag: `"v1"`, ICalUID: "uid", ResponseStatus: "organizer", Summary: "Review", StartAt: &start, EndAt: &end}
			draft := calendarProviderDraft(t, false)
			ctx := t.Context()

			entries := []struct {
				name string
				run  func()
			}{
				{"sync fetch", func() {
					_, _ = h.fetchCalendarSource(ctx, source, calendar.EventQuery{WindowStart: time.Now(), WindowEnd: time.Now().Add(24 * time.Hour)}, map[string]calendarCredentials{})
				}},
				{"create", func() { _, _ = h.createCalendarProviderEvent(ctx, source, draft) }},
				{"update", func() { _, _ = h.updateCalendarProviderEvent(ctx, source, event, draft, false) }},
				{"delete", func() { _, _ = h.deleteCalendarProviderEvent(ctx, source, event, false) }},
				{"series read", func() { _, _ = h.readCalendarProviderSeries(ctx, source, event) }},
				{"response read", func() { _, _ = h.readCalendarResponse(ctx, source, event, "") }},
				{"response write", func() {
					// handleRespond writes with a target that readCalendarResponse built
					// from the saved credentials; the context it uses carries no anchor.
					target := calendarResponseTarget{Endpoint: event.RemoteID, HTTPETag: `"v1"`, CalDAV: &calDAVResponseTarget{Username: "user", Password: "pass", BaseURL: remote}}
					desired, _ := calendarUpdateDecodeICS([]byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:uid\r\nDTSTAMP:20260101T000000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"))
					_, _ = putCalDAVResponse(ctx, target, desired, "accepted")
				}},
			}
			for _, entry := range entries {
				entry.run()
				allowed, denied := probe.take()
				if allowed+denied == 0 {
					t.Errorf("%s made no CalDAV request, so its anchor was not exercised", entry.name)
				}
				if mode.typedOK && denied != 0 || !mode.typedOK && allowed != 0 {
					t.Errorf("%s: %d requests allowed, %d denied; typed LAN server allowed=%v", entry.name, allowed, denied, mode.typedOK)
				}
			}
		})
	}
}

// Discovery anchors only the URL the person typed or saved.
func TestCalDAVDiscoveryAnchorsOnlyTheTypedURL(t *testing.T) {
	useDAVMode(t, auth.ModePersonal, nil)
	anchored := func(raw string) context.Context { return withCalDAVAnchor(context.Background(), raw) }
	for raw, want := range map[string]bool{
		"https://192.168.1.5:8443/dav/": true,
		"nas.local":                     true,  // typed without a scheme
		"":                              false, // autodiscovery from an email address
		"http://nas.local/":             false, // CalDAV base URLs must be https
	} {
		host := "192.168.1.5:8443"
		if raw == "nas.local" {
			host = "nas.local:443"
		}
		got := davPrivateTargetAllowed(anchored(raw), "https", hostOf(host), portOf(host))
		if got != want {
			t.Errorf("anchor %q allows its own host = %v, want %v", raw, got, want)
		}
	}
	// A host learned during discovery is not the typed one.
	ctx := anchored("https://192.168.1.5:8443/dav/")
	for _, target := range []string{"192.168.1.6:8443", "192.168.1.5:443", "10.0.0.1:8443"} {
		if davPrivateTargetAllowed(ctx, "https", hostOf(target), portOf(target)) {
			t.Errorf("%s allowed though only 192.168.1.5:8443 was typed", target)
		}
	}
}

func hostOf(hostPort string) string { h, _, _ := net.SplitHostPort(hostPort); return h }
func portOf(hostPort string) int {
	_, p, _ := net.SplitHostPort(hostPort)
	n, _ := strconv.Atoi(p)
	return n
}
