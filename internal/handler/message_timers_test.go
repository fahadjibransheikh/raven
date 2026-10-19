package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/auth"
	"github.com/fahadjibransheikh/raven/internal/mail"
	"github.com/fahadjibransheikh/raven/internal/storage"
)

func seedTimerMessage(t *testing.T, db *storage.DB) {
	t.Helper()
	if _, err := db.Write().ExecContext(t.Context(), `
		INSERT INTO folders (id, account_id, remote_id, name, role) VALUES ('owner-inbox', 'victim-account', 'INBOX', 'Inbox', 'inbox');
		INSERT INTO messages (id, account_id, internet_message_id, thread_id, subject, from_name, from_email, date_received)
		VALUES (7, 'victim-account', '<m7@x>', 't7', 'Quarterly numbers', 'Sam', 'sam@example.org', '2026-01-01 10:00:00');
		INSERT INTO message_folder_state (message_id, folder_id, remote_uid, is_read, synced_at) VALUES (7, 'owner-inbox', 7, 1, CURRENT_TIMESTAMP);`); err != nil {
		t.Fatal(err)
	}
	if err := db.RefreshFolderThreadState(t.Context(), "owner-inbox"); err != nil {
		t.Fatal(err)
	}
}

func timerRequest(method, path, body, user string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.SetPathValue("id", "7")
	return req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{ID: user, Username: user}))
}

func TestSnoozeEndpointValidatesOwnsAndClears(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	h.syncer = mail.NewSyncOrchestrator(db, h.accountStore, nil, nil)
	seedTimerMessage(t, db)
	snooze := h.handleSetMessageTimer(storage.MessageTimerSnooze)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	for name, body := range map[string]string{
		"past":       `{"until":"2020-01-01T00:00:00Z"}`,
		"too far":    `{"until":"` + time.Now().Add(400*24*time.Hour).UTC().Format(time.RFC3339) + `"}`,
		"not a time": `{"until":"tomorrow"}`,
	} {
		rec := httptest.NewRecorder()
		snooze(rec, timerRequest(http.MethodPost, "/api/messages/7/snooze", body, "owner"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", name, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	snooze(rec, timerRequest(http.MethodPost, "/api/messages/7/snooze", `{"until":"`+future+`"}`, "attacker"))
	if rec.Code == http.StatusOK {
		t.Fatal("another user snoozed the owner's message")
	}
	rec = httptest.NewRecorder()
	snooze(rec, timerRequest(http.MethodPost, "/api/messages/7/snooze", `{"until":"`+future+`"}`, "owner"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), future) {
		t.Fatalf("snooze status = %d body = %s", rec.Code, rec.Body.String())
	}
	if n, err := db.GetFolderEmailCountUnfiltered(t.Context(), "owner-inbox"); err != nil || n != 0 {
		t.Fatalf("inbox count while snoozed = %d, %v", n, err)
	}
	rec = httptest.NewRecorder()
	snooze(rec, timerRequest(http.MethodDelete, "/api/messages/7/snooze", "", "owner"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"cleared":true`) {
		t.Fatalf("clear status = %d body = %s", rec.Code, rec.Body.String())
	}
	if n, _ := db.GetFolderEmailCountUnfiltered(t.Context(), "owner-inbox"); n != 1 {
		t.Fatalf("inbox count after clear = %d", n)
	}
}

func TestMessageTimerWorkerWakesSnoozeAndSkipsAnsweredFollowUp(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	seedTimerMessage(t, db)
	set := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	if _, err := db.SetMessageTimer(t.Context(), "owner", 7, storage.MessageTimerSnooze, set.Add(time.Hour), set); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetMessageTimer(t.Context(), "owner", 7, storage.MessageTimerFollowUp, set.Add(time.Hour), set); err != nil {
		t.Fatal(err)
	}
	var events []mail.Event
	publish := func(e mail.Event) { events = append(events, e) }

	h.runMessageTimersIn(t.Context(), db, "", set.Add(30*time.Minute), publish)
	if len(events) != 0 {
		t.Fatalf("events before due = %#v", events)
	}
	h.runMessageTimersIn(t.Context(), db, "", set.Add(2*time.Hour), publish)
	var notices []string
	for _, e := range events {
		if e.Type == mail.EventNewMail {
			notices = append(notices, e.Payload["subject"].(string))
		}
	}
	if len(notices) != 2 || notices[0] != "Snoozed: Quarterly numbers" || notices[1] != "No reply yet: Quarterly numbers" {
		t.Fatalf("notices = %#v", notices)
	}
	if n, _ := db.GetFolderEmailCountUnfiltered(t.Context(), "owner-inbox"); n != 1 {
		t.Fatalf("inbox count after wake = %d", n)
	}

	// A follow-up whose thread got an answer ends quietly.
	if _, err := db.SetMessageTimer(t.Context(), "owner", 7, storage.MessageTimerFollowUp, set.Add(time.Hour), set); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().ExecContext(t.Context(), `INSERT INTO messages (id, account_id, internet_message_id, thread_id, subject, from_email, date_received)
		VALUES (8, 'victim-account', '<m8@x>', 't7', 'Re: Quarterly numbers', 'sam@example.org', '2026-01-01 11:30:00')`); err != nil {
		t.Fatal(err)
	}
	events = nil
	h.runMessageTimersIn(t.Context(), db, "", set.Add(2*time.Hour), publish)
	for _, e := range events {
		if e.Type == mail.EventNewMail {
			t.Fatalf("answered follow-up notified: %#v", e)
		}
	}
	if due, _ := db.DueMessageTimers(t.Context(), set.Add(2*time.Hour), 10); len(due) != 0 {
		t.Fatalf("answered follow-up left behind: %#v", due)
	}
}
