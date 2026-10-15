package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/auth"
	"github.com/fahadjibransheikh/raven/internal/config"
	mailpkg "github.com/fahadjibransheikh/raven/internal/mail"
	"github.com/fahadjibransheikh/raven/internal/mail/message"
	"github.com/fahadjibransheikh/raven/internal/mailauth"
	"github.com/fahadjibransheikh/raven/internal/providers"
	"github.com/fahadjibransheikh/raven/internal/storage"
	"github.com/fahadjibransheikh/raven/internal/store"
)

// runDueOutgoingSends dispatches workers for every account with due mail and
// waits for them, so tests can assert on the final state.
func (h *Handler) runDueOutgoingSends(ctx context.Context) {
	h.dispatchDueOutgoingSends(ctx)
	h.outgoingWorkers.Wait()
}

// gmailSendFixture is a set of Gmail accounts whose "transport" is a fake Gmail
// API. The bearer token identifies the account, so hooks can stall one account.
type gmailSendFixture struct {
	t      *testing.T
	path   string
	hook   func(token, messageID string, r *http.Request)
	mu     sync.Mutex
	counts map[string]int
}

func newGmailSendFixture(t *testing.T, accounts ...string) *gmailSendFixture {
	t.Helper()
	f := &gmailSendFixture{t: t, path: filepath.Join(t.TempDir(), "raven.db"), counts: map[string]int{}}
	db := f.openDB()
	ctx := context.Background()
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO users (id, username, username_normalized, name) VALUES ('default', 'default', 'default', 'Default')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	creds := mailauth.New(&mailauth.Config{}, db, testMailboxCredentialKey)
	expires := time.Now().Add(time.Hour)
	for _, id := range accounts {
		if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, user_id, provider, provider_account_id, email_address) VALUES (?, 'default', ?, ?, ?)`,
			id, providers.ProviderGmail, "sub-"+id, id+"@example.com"); err != nil {
			t.Fatalf("insert account: %v", err)
		}
		if err := creds.UpsertOAuthAccount(ctx, id, providers.OAuthGoogle, "sub-"+id, "token-"+id, "refresh-"+id, "Bearer", &expires, "https://mail.google.com/"); err != nil {
			t.Fatalf("UpsertOAuthAccount() error = %v", err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	previous := gmailAPIBaseURL
	gmailAPIBaseURL = server.URL
	t.Cleanup(func() { gmailAPIBaseURL = previous })
	return f
}

func (f *gmailSendFixture) openDB() *storage.DB {
	f.t.Helper()
	db, err := storage.New(f.path)
	if err != nil {
		f.t.Fatalf("storage.New() error = %v", err)
	}
	f.t.Cleanup(func() { _ = db.Close() })
	return db
}

// newHandler builds a handler over its own connection to the shared database,
// like a second Raven process would.
func (f *gmailSendFixture) newHandler(db *storage.DB) *Handler {
	f.t.Helper()
	accountStore, err := config.NewAccountStore(db, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		f.t.Fatalf("NewAccountStore() error = %v", err)
	}
	creds := mailauth.New(&mailauth.Config{}, db, testMailboxCredentialKey)
	return New(db, accountStore, mailpkg.NewSyncOrchestrator(db, accountStore, nil, nil), store.NewBlobStore(filepath.Join(f.t.TempDir(), "blobs")), auth.NewManager(&auth.Config{}, db), "", creds)
}

func (f *gmailSendFixture) serve(w http.ResponseWriter, r *http.Request) {
	var payload map[string]string
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload["raw"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	messageID := parsed.Header.Get("Message-Id")
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	f.counts[messageID]++
	f.mu.Unlock()
	if f.hook != nil {
		f.hook(token, messageID, r)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":"gmail-id"}`))
}

func (f *gmailSendFixture) deliveries(messageID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[messageID]
}

func queueTestSend(t *testing.T, h *Handler, account, name string) storage.OutgoingSend {
	t.Helper()
	to, _ := message.ParseAddressList("recipient@example.com")
	msg := &message.OutgoingMessage{
		FromEmail: account + "@example.com", To: to, Subject: name, TextBody: name,
		MessageID: "<" + name + "@example.com>", Date: time.Now().UTC(),
	}
	queued, err := h.queueOutgoingMessage(context.Background(), account, 0, "", msg, time.Now().Add(-time.Second), false)
	if err != nil {
		t.Fatalf("queueOutgoingMessage(%s) error = %v", name, err)
	}
	return queued
}

func waitForSendStatus(t *testing.T, db *storage.DB, id, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		send, err := db.GetOutgoingSend(context.Background(), id)
		if err == nil && send.Status == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("send %s status = %q (err %v), want %q", id, send.Status, err, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func requireSendStatus(t *testing.T, db *storage.DB, id, want string) {
	t.Helper()
	send, err := db.GetOutgoingSend(context.Background(), id)
	if err != nil || send.Status != want {
		t.Fatalf("send %s status = %q (err %v), want %q", id, send.Status, err, want)
	}
}

func TestStalledAccountDoesNotBlockOtherAccountsSends(t *testing.T) {
	f := newGmailSendFixture(t, "a", "b")
	db := f.openDB()
	h := f.newHandler(db)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) }) // a failed assert must not leave the fake server stuck
	f.hook = func(token, _ string, r *http.Request) {
		if token == "token-a" {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
	}
	a := queueTestSend(t, h, "a", "stalled")
	b := queueTestSend(t, h, "b", "healthy")

	h.dispatchDueOutgoingSends(context.Background())

	waitForSendStatus(t, db, b.ID, storage.OutgoingSendSent)
	requireSendStatus(t, db, a.ID, storage.OutgoingSendSending)
	releaseOnce.Do(func() { close(release) })
	h.outgoingWorkers.Wait()
	requireSendStatus(t, db, a.ID, storage.OutgoingSendSent)
}

func TestOutgoingDeliveryConcurrencyIsBoundedAndSerialPerAccount(t *testing.T) {
	const accounts, perAccount = 12, 3
	ids := make([]string, accounts)
	for i := range ids {
		ids[i] = fmt.Sprintf("acc%02d", i)
	}
	f := newGmailSendFixture(t, ids...)
	db := f.openDB()
	h := f.newHandler(db)
	var mu sync.Mutex
	active, maxActive := 0, 0
	activeByToken, maxByToken := map[string]int{}, 0
	f.hook = func(token, _ string, _ *http.Request) {
		mu.Lock()
		active++
		activeByToken[token]++
		maxActive = max(maxActive, active)
		maxByToken = max(maxByToken, activeByToken[token])
		mu.Unlock()
		time.Sleep(40 * time.Millisecond)
		mu.Lock()
		active--
		activeByToken[token]--
		mu.Unlock()
	}
	var sends []storage.OutgoingSend
	for _, id := range ids {
		for n := 0; n < perAccount; n++ {
			sends = append(sends, queueTestSend(t, h, id, fmt.Sprintf("%s-%d", id, n)))
		}
	}

	h.runDueOutgoingSends(context.Background())

	if maxActive != 8 { // spec: eight deliveries at once, overall
		t.Fatalf("max concurrent deliveries = %d, want 8", maxActive)
	}
	if maxByToken != 1 {
		t.Fatalf("max concurrent deliveries for one account = %d, want 1", maxByToken)
	}
	for _, send := range sends {
		requireSendStatus(t, db, send.ID, storage.OutgoingSendSent)
	}
}

func TestTwoDispatchersDeliverEachSendExactlyOnce(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "e", "f"}
	f := newGmailSendFixture(t, ids...)
	db1, db2 := f.openDB(), f.openDB()
	h1, h2 := f.newHandler(db1), f.newHandler(db2)
	f.hook = func(string, string, *http.Request) { time.Sleep(5 * time.Millisecond) }
	var sends []storage.OutgoingSend
	for _, id := range ids {
		for n := 0; n < 4; n++ {
			sends = append(sends, queueTestSend(t, h1, id, fmt.Sprintf("%s-%d", id, n)))
		}
	}

	var wg sync.WaitGroup
	for _, h := range []*Handler{h1, h2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Keep dispatching so the two sides race for the same rows until none are left.
			for i := 0; i < 20; i++ {
				h.dispatchDueOutgoingSends(context.Background())
				time.Sleep(5 * time.Millisecond)
			}
			h.outgoingWorkers.Wait()
		}()
	}
	wg.Wait()

	for _, send := range sends {
		requireSendStatus(t, db1, send.ID, storage.OutgoingSendSent)
		var snapshot outgoingMessageSnapshot
		if err := json.Unmarshal(send.MessageJSON, &snapshot); err != nil {
			t.Fatal(err)
		}
		if got := f.deliveries(snapshot.MessageID); got != 1 {
			t.Fatalf("%s delivered %d times, want exactly once", snapshot.MessageID, got)
		}
	}
}

func TestDispatcherCancelledMidSendLeavesSendAmbiguousAndNeverResends(t *testing.T) {
	f := newGmailSendFixture(t, "a", "stale")
	db := f.openDB()
	h := f.newHandler(db)
	inFlight := make(chan struct{})
	f.hook = func(_, _ string, r *http.Request) {
		close(inFlight)
		<-r.Context().Done() // the server never answers; only the client giving up ends it
	}
	// A send a previous process left "sending" must be recovered before any worker starts.
	stale := queueTestSend(t, h, "stale", "left-sending")
	if _, err := db.Write().ExecContext(context.Background(), `UPDATE outgoing_sends SET status = ? WHERE id = ?`, storage.OutgoingSendSending, stale.ID); err != nil {
		t.Fatal(err)
	}
	a := queueTestSend(t, h, "a", "in-flight")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.runOutgoingDispatcher(ctx)
		close(done)
	}()
	select {
	case <-inFlight:
	case <-time.After(10 * time.Second):
		t.Fatal("send never reached the transport")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("dispatcher did not return after cancel")
	}

	requireSendStatus(t, db, a.ID, storage.OutgoingSendAmbiguous)
	requireSendStatus(t, db, stale.ID, storage.OutgoingSendAmbiguous)
	if got := f.deliveries("<in-flight@example.com>"); got != 1 {
		t.Fatalf("in-flight send reached the transport %d times, want 1", got)
	}
	if got := f.deliveries("<left-sending@example.com>"); got != 0 {
		t.Fatalf("stale send was delivered %d times, want 0", got)
	}
}

func TestDispatcherWaitsForInFlightDeliveryOnShutdown(t *testing.T) {
	f := newGmailSendFixture(t, "a")
	db := f.openDB()
	h := f.newHandler(db)
	send := queueTestSend(t, h, "a", "waits")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	// deliverOutgoingSend reads the clock first, after the send is claimed.
	h.outgoingNow = func() time.Time {
		once.Do(func() {
			close(entered)
			<-release
		})
		return time.Now()
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.runOutgoingDispatcher(ctx)
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("delivery never started")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("dispatcher returned while a delivery was still in flight")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("dispatcher did not return after the delivery ended")
	}
	got, err := db.GetOutgoingSend(context.Background(), send.ID)
	// The delivery ran with a cancelled context: it must have recorded a final
	// state (never left "sending" or put back to "pending" for a resend).
	if err != nil || got.Status == storage.OutgoingSendPending || got.Status == storage.OutgoingSendSending {
		t.Fatalf("send after shutdown = %#v, %v", got, err)
	}
}

func TestWakeWhileWorkerRunningDeliversNewSend(t *testing.T) {
	f := newGmailSendFixture(t, "a")
	db := f.openDB()
	h := f.newHandler(db)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	entered := make(chan struct{})
	var once sync.Once
	f.hook = func(_, messageID string, r *http.Request) {
		if messageID != "<first@example.com>" {
			return
		}
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}
	first := queueTestSend(t, h, "a", "first")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.runOutgoingDispatcher(ctx)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first send never reached the transport")
	}

	// The account's worker is busy; the new send arrives on a wake.
	second := queueTestSend(t, h, "a", "second")
	h.signalOutgoingWorker()
	time.Sleep(100 * time.Millisecond) // let the dispatcher see the wake while the worker still runs
	releaseOnce.Do(func() { close(release) })

	waitForSendStatus(t, db, first.ID, storage.OutgoingSendSent)
	waitForSendStatus(t, db, second.ID, storage.OutgoingSendSent)
	if got := f.deliveries("<second@example.com>"); got != 1 {
		t.Fatalf("second send delivered %d times, want 1", got)
	}
}
