package handler

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/storage"
)

func TestRavenCalendarCarryoverTickKeepsSelectionUntilDiscoverySucceeds(t *testing.T) {
	db, err := storage.New(filepath.Join(t.TempDir(), "raven.db"))
	if err != nil {
		t.Fatalf("storage.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Write().Exec(`
		INSERT INTO users (id, username, username_normalized, name)
		VALUES ('default', 'default', 'default', 'Default');
		INSERT INTO accounts (id, user_id, provider, provider_account_id, email_address, display_name, auth_method)
		VALUES ('outlook-account', 'default', 'outlook', 'subject-id', 'person@outlook.com', 'Person', 'oauth2');
		INSERT INTO raven_calendar_carryover (account_id, remote_id, selected) VALUES ('outlook-account', 'cal', 1);`); err != nil {
		t.Fatalf("insert test account: %v", err)
	}
	// No OAuth configured: discovery fails, so the selection must survive.
	h := &Handler{db: db}
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	h.runRavenCalendarCarryoverTick(t.Context(), now)
	waiting, err := db.RavenCalendarCarryoverAccounts(t.Context())
	if err != nil || len(waiting) != 1 {
		t.Fatalf("carryover after failed discovery = %v, %v; want it kept", waiting, err)
	}
	if want := now.Add(ravenCalendarCarryoverRetry); !h.ravenCarryoverNextAttempt.Equal(want) {
		t.Fatalf("next attempt = %v, want %v", h.ravenCarryoverNextAttempt, want)
	}
	// Ticks before the retry time do nothing, even if the data changed.
	if _, err := db.Write().Exec(`DELETE FROM raven_calendar_carryover`); err != nil {
		t.Fatal(err)
	}
	h.runRavenCalendarCarryoverTick(t.Context(), now.Add(time.Minute))
	if want := now.Add(ravenCalendarCarryoverRetry); !h.ravenCarryoverNextAttempt.Equal(want) {
		t.Fatalf("tick before retry time ran: next attempt = %v", h.ravenCarryoverNextAttempt)
	}
	// Once nothing is waiting, the worker checks again only a day later.
	later := now.Add(ravenCalendarCarryoverRetry)
	h.runRavenCalendarCarryoverTick(t.Context(), later)
	if want := later.Add(24 * time.Hour); !h.ravenCarryoverNextAttempt.Equal(want) {
		t.Fatalf("idle next attempt = %v, want %v", h.ravenCarryoverNextAttempt, want)
	}
}
