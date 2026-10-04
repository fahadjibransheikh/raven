package mail

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/providers"
	"github.com/cristianadrielbraun/gofer/internal/storage"
)

const sendAsFixture = `{"sendAs":[
 {"sendAsEmail":"user@example.com","isPrimary":true,"displayName":"User"},
 {"sendAsEmail":"Work@example.com","displayName":"Work","verificationStatus":"accepted","isDefault":true},
 {"sendAsEmail":"pending@example.com","verificationStatus":"pending"}
]}`

func newGmailIdentityFixture(t *testing.T, body string) (*SyncOrchestrator, *storage.DB, *atomic.Int32) {
	t.Helper()
	db := newLabelSyncTestDB(t)
	if _, err := db.Write().Exec(`INSERT INTO accounts (id, user_id, provider, email_address, display_name) VALUES ('acc', 'default', ?, 'user@example.com', 'User')`, providers.ProviderGmail); err != nil {
		t.Fatal(err)
	}
	tx, _ := db.Write().Begin()
	if err := storage.SyncPrimaryIdentityTx(context.Background(), tx, "acc"); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/me/settings/sendAs" || r.Header.Get("Authorization") != "Bearer gmail-token" {
			t.Errorf("unexpected request %s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		calls.Add(1)
		if body == "" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	prev := gmailAPIBaseURL
	gmailAPIBaseURL = server.URL
	t.Cleanup(func() { gmailAPIBaseURL = prev })
	return NewSyncOrchestrator(db, nil, nil, labelSyncTestTokens{}), db, calls
}

func TestRefreshGmailIdentitiesAcceptsOnlyAcceptedAliasesAndKeepsManual(t *testing.T) {
	ctx := context.Background()
	o, db, _ := newGmailIdentityFixture(t, sendAsFixture)
	if _, err := db.AddManualIdentity(ctx, "default", "acc", "mine@example.com", ""); err != nil {
		t.Fatal(err)
	}
	// A provider identity Gmail no longer returns.
	if err := db.ApplyProviderIdentities(ctx, "acc", []storage.ProviderIdentity{{Email: "gone@example.com"}}, "", time.Now()); err != nil {
		t.Fatal(err)
	}

	if err := o.RefreshGmailIdentities(ctx, "acc"); err != nil {
		t.Fatal(err)
	}
	ids, _ := db.ListAccountIdentities(ctx, "default", "acc")
	got := map[string]string{}
	var defaults []string
	for _, id := range ids {
		got[id.Email] = id.Source
		if id.IsDefault {
			defaults = append(defaults, id.Email)
		}
	}
	want := map[string]string{"user@example.com": "primary", "work@example.com": "provider", "mine@example.com": "manual"}
	if len(got) != len(want) {
		t.Fatalf("identities = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("identities = %v, want %v", got, want)
		}
	}
	if len(defaults) != 1 || defaults[0] != "work@example.com" {
		t.Fatalf("defaults = %v, want Gmail's default work@example.com", defaults)
	}
}

func TestRefreshGmailIdentitiesRespectsExplicitDefault(t *testing.T) {
	ctx := context.Background()
	o, db, _ := newGmailIdentityFixture(t, sendAsFixture)
	manual, _ := db.AddManualIdentity(ctx, "default", "acc", "mine@example.com", "")
	if err := db.SetDefaultIdentity(ctx, "default", "acc", manual.ID); err != nil {
		t.Fatal(err)
	}
	if err := o.RefreshGmailIdentities(ctx, "acc"); err != nil {
		t.Fatal(err)
	}
	m, _ := db.IdentityForAccount(ctx, "default", "acc", "mine@example.com")
	if !m.IsDefault {
		t.Fatal("Gmail's default overrode the user's explicit default")
	}
}

func TestRefreshGmailIdentitiesIfDueThrottlesTo24Hours(t *testing.T) {
	ctx := context.Background()
	o, db, calls := newGmailIdentityFixture(t, sendAsFixture)

	o.refreshGmailIdentitiesIfDue(ctx, "acc") // never fetched
	o.refreshGmailIdentitiesIfDue(ctx, "acc") // just fetched
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	if err := db.TouchIdentitiesSyncedAt(ctx, "acc", time.Now().Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	o.refreshGmailIdentitiesIfDue(ctx, "acc")
	if calls.Load() != 2 {
		t.Fatalf("calls after 25h = %d, want 2", calls.Load())
	}
	if err := db.TouchIdentitiesSyncedAt(ctx, "acc", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	o.refreshGmailIdentitiesIfDue(ctx, "acc")
	if calls.Load() != 2 {
		t.Fatalf("calls after 1h = %d, want still 2", calls.Load())
	}
}

func TestRefreshGmailIdentitiesFailureIsNonFatalAndThrottled(t *testing.T) {
	ctx := context.Background()
	o, db, calls := newGmailIdentityFixture(t, "")

	o.refreshGmailIdentitiesIfDue(ctx, "acc") // logs, does not panic or return an error
	o.refreshGmailIdentitiesIfDue(ctx, "acc")
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want the failed attempt to be throttled", calls.Load())
	}
	ids, _ := db.ListAccountIdentities(ctx, "default", "acc")
	if len(ids) != 1 || ids[0].Source != "primary" {
		t.Fatalf("identities changed by a failed fetch: %+v", ids)
	}
	if err := o.RefreshGmailIdentities(ctx, "acc"); err == nil {
		t.Fatal("manual refresh should surface the error")
	}
}
