package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

func newIdentityTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := New(filepath.Join(t.TempDir(), "gofer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if _, err := db.Write().ExecContext(ctx, `
		INSERT INTO users (id, username, username_normalized, name) VALUES ('u1','user1','user1','U1'),('u2','user2','user2','U2');
		INSERT INTO accounts (id, user_id, provider, email_address, display_name) VALUES
		  ('a1','u1','gmail','Me@Example.com','Me'),
		  ('a2','u2','imap','other@example.com','Other')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a1", "a2"} {
		syncPrimary(t, db, id)
	}
	return db
}

func syncPrimary(t *testing.T, db *DB, accountID string) {
	t.Helper()
	tx, err := db.Write().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := SyncPrimaryIdentityTx(context.Background(), tx, accountID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func defaultCount(t *testing.T, db *DB, accountID string) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRow(`SELECT COUNT(*) FROM account_identities WHERE account_id = ? AND is_default = 1`, accountID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMigrateV95ToV96BackfillsPrimaryIdentityAndIsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gofer.db")
	raw, err := openDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP);
		INSERT INTO schema_version (version) VALUES (95);
		CREATE TABLE accounts (id TEXT PRIMARY KEY, email_address TEXT NOT NULL, display_name TEXT NOT NULL DEFAULT '');
		INSERT INTO accounts (id, email_address, display_name) VALUES ('a1', ' A@Example.com ', 'Fahad'), ('a2', 'b@example.com', '')`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	db, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rows, err := db.Read().Query(`SELECT account_id, email, name, source, is_default FROM account_identities ORDER BY account_id`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var a, e, n, s string
		var d int
		_ = rows.Scan(&a, &e, &n, &s, &d)
		got = append(got, fmt.Sprintf("%s|%s|%s|%s|%d", a, e, n, s, d))
	}
	rows.Close()
	want := []string{"a1|a@example.com|Fahad|primary|1", "a2|b@example.com||primary|1"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("identities = %v, want %v", got, want)
	}
	// Re-running must not duplicate or fail.
	tx, _ := db.Write().Begin()
	defer tx.Rollback()
	if err := migrateV95ToV96(tx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	var n int
	_ = tx.QueryRow(`SELECT COUNT(*) FROM account_identities`).Scan(&n)
	if n != 2 {
		t.Fatalf("identity count after rerun = %d, want 2", n)
	}
}

func TestIdentityInvariants(t *testing.T) {
	ctx := context.Background()
	db := newIdentityTestDB(t)

	ids, _ := db.ListAccountIdentities(ctx, "u1", "a1")
	if len(ids) != 1 || ids[0].Source != "primary" || !ids[0].IsDefault || ids[0].Email != "me@example.com" {
		t.Fatalf("initial identities = %+v", ids)
	}
	primaryID := ids[0].ID

	manual, err := db.AddManualIdentity(ctx, "u1", "a1", "  Alias@Example.com ", "Alias")
	if err != nil || manual.Email != "alias@example.com" || manual.Source != "manual" || manual.IsDefault {
		t.Fatalf("add manual = %+v, %v", manual, err)
	}
	for _, dup := range []string{"alias@example.com", "ME@example.com"} {
		if _, err := db.AddManualIdentity(ctx, "u1", "a1", dup, ""); !errors.Is(err, ErrIdentityDuplicate) {
			t.Fatalf("add %q err = %v, want duplicate", dup, err)
		}
	}
	if _, err := db.AddManualIdentity(ctx, "u1", "a1", "not an address", ""); !errors.Is(err, ErrIdentityInvalid) {
		t.Fatalf("invalid err = %v", err)
	}

	if err := db.SetDefaultIdentity(ctx, "u1", "a1", manual.ID); err != nil {
		t.Fatal(err)
	}
	if n := defaultCount(t, db, "a1"); n != 1 {
		t.Fatalf("defaults = %d, want exactly 1", n)
	}
	got, _ := db.IdentityForAccount(ctx, "u1", "a1", "ALIAS@example.com")
	if !got.IsDefault {
		t.Fatal("alias should be the default")
	}
	// Deleting the default hands it back to the primary.
	if err := db.DeleteIdentity(ctx, "u1", "a1", manual.ID); err != nil {
		t.Fatal(err)
	}
	p, _ := db.IdentityForAccount(ctx, "u1", "a1", "me@example.com")
	if !p.IsDefault || defaultCount(t, db, "a1") != 1 {
		t.Fatal("default did not return to primary")
	}
	if err := db.DeleteIdentity(ctx, "u1", "a1", primaryID); !errors.Is(err, ErrIdentityProtected) {
		t.Fatalf("delete primary err = %v", err)
	}
}

func TestIdentityOwnershipScoping(t *testing.T) {
	ctx := context.Background()
	db := newIdentityTestDB(t)
	mine, _ := db.AddManualIdentity(ctx, "u1", "a1", "alias@example.com", "")

	if _, err := db.AddManualIdentity(ctx, "u2", "a1", "evil@example.com", ""); !errors.Is(err, ErrIdentityNotFound) {
		t.Fatalf("foreign add err = %v", err)
	}
	if err := db.DeleteIdentity(ctx, "u2", "a1", mine.ID); !errors.Is(err, ErrIdentityNotFound) {
		t.Fatalf("foreign delete err = %v", err)
	}
	if err := db.SetDefaultIdentity(ctx, "u2", "a1", mine.ID); !errors.Is(err, ErrIdentityNotFound) {
		t.Fatalf("foreign set default err = %v", err)
	}
	if _, err := db.IdentityForAccount(ctx, "u2", "a1", "alias@example.com"); !errors.Is(err, ErrIdentityNotFound) {
		t.Fatalf("foreign lookup err = %v", err)
	}
	if err := db.DismissIdentitySuggestion(ctx, "u2", "a1", "x@example.com"); !errors.Is(err, ErrIdentityNotFound) {
		t.Fatalf("foreign dismiss err = %v", err)
	}
	// An identity id from another account must not be reachable through your own account.
	if err := db.DeleteIdentity(ctx, "u2", "a2", mine.ID); !errors.Is(err, ErrIdentityNotFound) {
		t.Fatalf("cross-account delete err = %v", err)
	}
	if ids, _ := db.ListAccountIdentities(ctx, "u2", "a1"); len(ids) != 0 {
		t.Fatalf("foreign list = %+v", ids)
	}
	all, _ := db.ListUserIdentities(ctx, "u1")
	if len(all) != 1 || len(all["a1"]) != 2 {
		t.Fatalf("user identities = %+v", all)
	}
}

func TestPrimaryIdentityFollowsAccountEdits(t *testing.T) {
	ctx := context.Background()
	db := newIdentityTestDB(t)
	alias, _ := db.AddManualIdentity(ctx, "u1", "a1", "new@example.com", "")
	_ = db.SetDefaultIdentity(ctx, "u1", "a1", alias.ID)

	if _, err := db.Write().Exec(`UPDATE accounts SET email_address = 'New@Example.com', display_name = 'Renamed' WHERE id = 'a1'`); err != nil {
		t.Fatal(err)
	}
	syncPrimary(t, db, "a1")
	ids, _ := db.ListAccountIdentities(ctx, "u1", "a1")
	if len(ids) != 1 || ids[0].Source != "primary" || ids[0].Email != "new@example.com" || ids[0].Name != "Renamed" || !ids[0].IsDefault {
		t.Fatalf("after edit = %+v (alias absorbed, default inherited)", ids)
	}
}

func TestApplyProviderIdentities(t *testing.T) {
	ctx := context.Background()
	db := newIdentityTestDB(t)
	manual, _ := db.AddManualIdentity(ctx, "u1", "a1", "mine@example.com", "Mine")
	now := time.Now()

	aliases := []ProviderIdentity{{Email: "me@example.com"}, {Email: "work@example.com", Name: "Work"}, {Email: "old@example.com"}, {Email: "mine@example.com", Name: "Hijack"}}
	if err := db.ApplyProviderIdentities(ctx, "a1", aliases, "work@example.com", now); err != nil {
		t.Fatal(err)
	}
	work, _ := db.IdentityForAccount(ctx, "u1", "a1", "work@example.com")
	if work.Source != "provider" || work.Name != "Work" || !work.IsDefault || defaultCount(t, db, "a1") != 1 {
		t.Fatalf("work = %+v", work)
	}
	m, _ := db.IdentityForAccount(ctx, "u1", "a1", "mine@example.com")
	if m.ID != manual.ID || m.Source != "manual" || m.Name != "Mine" {
		t.Fatalf("manual identity was touched: %+v", m)
	}
	if _, ok, _ := db.IdentitiesSyncedAt(ctx, "a1"); !ok {
		t.Fatal("synced_at not recorded")
	}

	// User picks a default; provider no longer overrides it. old@ disappears.
	_ = db.SetDefaultIdentity(ctx, "u1", "a1", manual.ID)
	if err := db.ApplyProviderIdentities(ctx, "a1", []ProviderIdentity{{Email: "work@example.com"}}, "work@example.com", now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.IdentityForAccount(ctx, "u1", "a1", "old@example.com"); !errors.Is(err, ErrIdentityNotFound) {
		t.Fatal("stale provider identity should be removed")
	}
	m, _ = db.IdentityForAccount(ctx, "u1", "a1", "mine@example.com")
	if !m.IsDefault || defaultCount(t, db, "a1") != 1 {
		t.Fatal("explicit default was overridden by provider")
	}

	// A provider default that vanishes falls back to primary.
	_ = db.DeleteIdentity(ctx, "u1", "a1", manual.ID)
	_ = db.ApplyProviderIdentities(ctx, "a1", []ProviderIdentity{{Email: "work@example.com"}}, "work@example.com", now)
	if err := db.ApplyProviderIdentities(ctx, "a1", nil, "", now); err != nil {
		t.Fatal(err)
	}
	p, _ := db.IdentityForAccount(ctx, "u1", "a1", "me@example.com")
	if !p.IsDefault || defaultCount(t, db, "a1") != 1 {
		t.Fatal("default did not fall back to primary")
	}
}

func seedReceived(t *testing.T, db *DB, accountID, from string, to ...string) {
	t.Helper()
	res, err := db.Write().Exec(`INSERT INTO messages (account_id, from_email) VALUES (?, ?)`, accountID, from)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	for _, r := range to {
		if _, err := db.Write().Exec(`INSERT INTO message_recipients (message_id, kind, email) VALUES (?, 'to', ?)`, id, r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIdentitySuggestions(t *testing.T) {
	ctx := context.Background()
	db := newIdentityTestDB(t)
	for i := 0; i < 3; i++ {
		seedReceived(t, db, "a1", "friend@x.com", "Hello@Example.com", "me@example.com")
	}
	for i := 0; i < 2; i++ { // below threshold
		seedReceived(t, db, "a1", "friend@x.com", "rare@example.com")
	}
	for i := 0; i < 4; i++ { // already an identity
		seedReceived(t, db, "a1", "friend@x.com", "known@example.com")
	}
	for i := 0; i < 5; i++ { // sent by me: not received
		seedReceived(t, db, "a1", "me@example.com", "someone@x.com")
	}
	if _, err := db.AddManualIdentity(ctx, "u1", "a1", "known@example.com", ""); err != nil {
		t.Fatal(err)
	}

	got, err := db.IdentitySuggestions(ctx, "u1", "a1")
	if err != nil || len(got) != 1 || got[0].Email != "hello@example.com" || got[0].Count != 3 {
		t.Fatalf("suggestions = %+v, %v (want only hello@example.com x3)", got, err)
	}
	if other, _ := db.IdentitySuggestions(ctx, "u2", "a1"); len(other) != 0 {
		t.Fatalf("foreign suggestions = %+v", other)
	}

	if err := db.DismissIdentitySuggestion(ctx, "u1", "a1", "Hello@example.com"); err != nil {
		t.Fatal(err)
	}
	seedReceived(t, db, "a1", "friend@x.com", "hello@example.com") // more traffic must not resurrect it
	if got, _ := db.IdentitySuggestions(ctx, "u1", "a1"); len(got) != 0 {
		t.Fatalf("dismissed suggestion reappeared: %+v", got)
	}
}

func TestIdentitySuggestionsCapAtFive(t *testing.T) {
	ctx := context.Background()
	db := newIdentityTestDB(t)
	for a := 0; a < 7; a++ {
		for i := 0; i < 3; i++ {
			seedReceived(t, db, "a1", "friend@x.com", fmt.Sprintf("addr%d@example.com", a))
		}
	}
	got, _ := db.IdentitySuggestions(ctx, "u1", "a1")
	if len(got) != MaxIdentitySuggestions {
		t.Fatalf("len = %d, want %d", len(got), MaxIdentitySuggestions)
	}
	var _ = models.AccountIdentity{}
}
