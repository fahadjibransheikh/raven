package providerquota

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock lets the budget's waits pass instantly.
type fakeClock struct{ now time.Time }

func testBudget() (*budget, *fakeClock) {
	clock := &fakeClock{now: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)}
	b := newBudget()
	b.now = func() time.Time { return clock.now }
	b.sleep = func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		clock.now = clock.now.Add(d)
		return nil
	}
	return b, clock
}

func TestGmailCostsMatchGmailQuotaUnits(t *testing.T) {
	for _, c := range []struct {
		method, path string
		cost         float64
	}{
		{http.MethodGet, "/gmail/v1/users/me/profile", 1},
		{http.MethodGet, "/gmail/v1/users/me/history", 2},
		{http.MethodGet, "/gmail/v1/users/me/labels", 1},
		{http.MethodPost, "/gmail/v1/users/me/labels", 5},
		{http.MethodGet, "/gmail/v1/users/me/messages", 5},
		{http.MethodGet, "/gmail/v1/users/me/messages/abc", 5},
		{http.MethodPost, "/gmail/v1/users/me/messages/abc/modify", 5},
		{http.MethodGet, "/gmail/v1/users/me/messages/abc/attachments/def", 5},
		{http.MethodDelete, "/gmail/v1/users/me/messages/abc", 10},
		{http.MethodPost, "/gmail/v1/users/me/messages/send", 100},
		{http.MethodPost, "/gmail/v1/users/me/messages/batchModify", 50},
		{http.MethodPost, "/gmail/v1/users/me/drafts", 10},
		{http.MethodPut, "/gmail/v1/users/me/drafts/abc", 15},
		{http.MethodPost, "/gmail/v1/users/me/drafts/send", 100},
	} {
		if got := gmailCost(c.method, c.path); got != c.cost {
			t.Errorf("%s %s costs %v, want %v", c.method, c.path, got, c.cost)
		}
	}
}

func TestBackgroundWorkLeavesRoomForTheReader(t *testing.T) {
	b, clock := testBudget()
	m := b.mailbox("token")
	start := clock.now
	// Background work spends down to the reserve, then waits.
	for range 100 {
		if err := b.wait(context.Background(), m, 5, true); err != nil {
			t.Fatal(err)
		}
	}
	if m.level < 0 {
		t.Fatalf("level went negative: %v", m.level)
	}
	if clock.now == start {
		t.Fatal("background work never waited")
	}
	// The reader's request goes out at once from what background work left.
	before := clock.now
	if err := b.wait(context.Background(), m, 5, false); err != nil {
		t.Fatal(err)
	}
	if clock.now != before {
		t.Fatal("the reader waited behind background work")
	}
}

func TestRefusalSlowsTheMailboxAndItRecovers(t *testing.T) {
	b, clock := testBudget()
	m := b.mailbox("token")
	b.refused(m, time.Time{})
	if m.rate != gmailUnitsPerMinute/2 {
		t.Fatalf("rate after refusal = %v, want half", m.rate)
	}
	// Background work waits out the pause.
	start := clock.now
	if err := b.wait(context.Background(), m, 5, true); err != nil {
		t.Fatal(err)
	}
	if clock.now.Sub(start) < gmailRefusalPause {
		t.Fatalf("background work resumed after %v, want at least %v", clock.now.Sub(start), gmailRefusalPause)
	}
	// A second refusal halves the pace again, down to a floor.
	for range 10 {
		b.refused(m, time.Time{})
	}
	if m.rate != gmailMinUnitsPerMinute {
		t.Fatalf("rate after many refusals = %v, want the floor", m.rate)
	}
	// Without refusals the pace grows back to the target.
	clock.now = clock.now.Add(20 * time.Minute)
	m.mu.Lock()
	m.refill(clock.now)
	m.mu.Unlock()
	if m.rate != gmailUnitsPerMinute {
		t.Fatalf("rate after 20 quiet minutes = %v, want %v", m.rate, gmailUnitsPerMinute)
	}
	// A Retry-After longer than the pause is respected.
	b.refused(m, clock.now.Add(5*time.Minute))
	if !m.pausedUntil.Equal(clock.now.Add(5 * time.Minute)) {
		t.Fatalf("pause ends %v, want the Retry-After time", m.pausedUntil)
	}
}

func TestWaitStopsWhenTheRequestIsCancelled(t *testing.T) {
	b, _ := testBudget()
	m := b.mailbox("token")
	b.refused(m, time.Time{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.wait(ctx, m, 5, true); err == nil {
		t.Fatal("wait ignored cancellation")
	}
}

func TestTokensOfOneMailboxShareABudget(t *testing.T) {
	b, _ := testBudget()
	b.bind("old-token", "ana/acc")
	b.bind("new-token", "ana/acc")
	if b.mailbox("old-token") != b.mailbox("new-token") {
		t.Fatal("a refreshed token got a budget of its own")
	}
	if b.mailbox("other-token") == b.mailbox("old-token") {
		t.Fatal("an unbound token shared another mailbox's budget")
	}
}

func TestRequestsSlowDownOnlyOnQuotaRefusals(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/gmail/v1/users/me/messages/quota":
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":{"code":403,"errors":[{"reason":"rateLimitExceeded"}],"status":"PERMISSION_DENIED"}}`)
		case "/gmail/v1/users/me/messages/denied":
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":{"code":403,"errors":[{"reason":"insufficientPermissions"}]}}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer server.Close()
	host, _ := url.Parse(server.URL)
	previous := GmailHost
	GmailHost = host.Host
	t.Cleanup(func() { GmailHost = previous })

	b, _ := testBudget()
	get := func(path, token string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, server.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := b.do(req, http.DefaultClient.Do)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// A permission error is passed on untouched and doesn't slow anything.
	resp := get("/gmail/v1/users/me/messages/denied", "ana-token")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "insufficientPermissions") {
		t.Fatalf("permission error reached the caller as %d %q", resp.StatusCode, body)
	}
	if m := b.mailbox("ana-token"); m.rate != gmailUnitsPerMinute {
		t.Fatalf("permission error changed the pace to %v", m.rate)
	}

	// A quota refusal is passed on too, and halves the mailbox's pace.
	resp = get("/gmail/v1/users/me/messages/quota", "ana-token")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "rateLimitExceeded") {
		t.Fatalf("quota error body = %q", body)
	}
	if m := b.mailbox("ana-token"); m.rate != gmailUnitsPerMinute/2 {
		t.Fatalf("pace after a quota refusal = %v", m.rate)
	}
	// Another mailbox keeps its pace.
	if m := b.mailbox("leo-token"); m.rate != gmailUnitsPerMinute {
		t.Fatalf("another mailbox slowed down to %v", m.rate)
	}

	// Requests to other hosts pass straight through.
	GmailHost = "gmail.example.invalid"
	before := requests.Load()
	resp = get("/anything", "ana-token")
	resp.Body.Close()
	if requests.Load() != before+1 {
		t.Fatal("request to another host was not sent")
	}
}

func TestUnusedBudgetsAreForgotten(t *testing.T) {
	b, clock := testBudget()
	b.mailbox("expired-token")
	clock.now = clock.now.Add(3 * time.Hour)
	b.mailbox("fresh-token")
	if len(b.mailboxes) != 1 {
		t.Fatalf("budgets kept = %d, want only the one in use", len(b.mailboxes))
	}
}

func TestDoGivesUpOnAServerThatNeverAnswers(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		select {
		case <-release:
		case <-time.After(3 * time.Second):
		}
	}))
	defer server.Close()
	defer close(release)
	previous := requestTimeout
	requestTimeout = 100 * time.Millisecond
	defer func() { requestTimeout = previous }()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp, err := Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("Do returned a response from a server that never answered")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Do took %s to give up", elapsed)
	}
}
