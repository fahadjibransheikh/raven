package notifications

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/mail"
	"github.com/fahadjibransheikh/raven/internal/storage"
)

// The managed-mode worker finds a due snooze in the owner's own store, puts the
// thread back in her inbox and raises a notification only for her.
func TestUserStorageSnoozeWakesInOwnersStore(t *testing.T) {
	f := newUserStorageFixtureMode(t, true)
	inboxCount := func() int {
		var n int
		if err := f.routing.WithUser(t.Context(), "alice", func(db *storage.DB) error {
			var err error
			n, err = db.GetFolderEmailCountUnfiltered(t.Context(), "alice-inbox")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := f.routing.WithUser(t.Context(), "alice", func(db *storage.DB) error {
		if _, err := db.Write().ExecContext(t.Context(), `
			INSERT INTO messages (id, account_id, internet_message_id, thread_id, subject, from_email, date_received)
			VALUES (1, ?, '<a1@x>', 'ta', 'Lease renewal', 'agent@example.org', '2026-01-01 10:00:00');
			INSERT INTO message_folder_state (message_id, folder_id, remote_uid, is_read, synced_at) VALUES (1, 'alice-inbox', 1, 1, CURRENT_TIMESTAMP)`, f.accounts["alice"].ID); err != nil {
			return err
		}
		return db.RefreshFolderThreadState(t.Context(), "alice-inbox")
	}); err != nil {
		t.Fatal(err)
	}
	until := time.Now().UTC().Add(2 * time.Second).Format(time.RFC3339)
	if rec := f.request("alice", http.MethodPost, "/api/messages/1/snooze", `{"until":"`+until+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("snooze: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.request("bob", http.MethodPost, "/api/messages/1/snooze", `{"until":"`+until+`"}`); rec.Code == http.StatusOK {
		t.Fatal("bob snoozed alice's message")
	}
	if n := inboxCount(); n != 0 {
		t.Fatalf("inbox while snoozed = %d", n)
	}

	events := f.events.Subscribe()
	defer f.events.Unsubscribe(events)
	time.Sleep(3 * time.Second)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.base.StartMessageTimerWorker(ctx) // runs a pass at once
	deadline := time.After(10 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Type != mail.EventNewMail {
				continue
			}
			if e.UserID != "alice" || e.Payload["subject"] != "Snoozed: Lease renewal" {
				t.Fatalf("notification = %#v", e)
			}
			if n := inboxCount(); n != 1 {
				t.Fatalf("inbox after wake = %d", n)
			}
			return
		case <-deadline:
			t.Fatal("no wake notification")
		}
	}
}
