package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func newCalendarReminderDB(t *testing.T) *DB {
	t.Helper()
	db, err := New(filepath.Join(t.TempDir(), "raven.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Write().Exec(`
		INSERT INTO users (id, username, username_normalized) VALUES ('rem-user', 'rem-user', 'rem-user');
		INSERT INTO accounts (id, user_id, provider, email_address) VALUES ('rem-account', 'rem-user', 'gmail', 'rem@example.com');`); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceCalendarSources(t.Context(), "rem-user", "rem-account", "gmail", []CalendarSource{
		{ID: "rem-source", RemoteID: "primary", Name: "Work", IsSelected: true},
	}); err != nil {
		t.Fatal(err)
	}
	return db
}

func timedReminderEvent(remoteID string, start time.Time) CalendarEvent {
	end := start.Add(30 * time.Minute)
	return CalendarEvent{RemoteID: remoteID, Summary: remoteID, Location: "Room 1", StartAt: &start, EndAt: &end}
}

func replaceReminderEvents(t *testing.T, db *DB, now time.Time, events ...CalendarEvent) {
	t.Helper()
	if err := db.ReplaceCalendarEvents(t.Context(), "rem-user", "rem-source", events, now.Add(-24*time.Hour), now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func reminderRemoteIDs(t *testing.T, db *DB, now time.Time) []string {
	t.Helper()
	reminders, err := db.UpcomingCalendarReminders(t.Context(), now, 15*time.Minute, 50)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range reminders {
		if r.UserID != "rem-user" || r.AccountID != "rem-account" || r.SourceID != "rem-source" {
			t.Fatalf("reminder owner = %#v", r)
		}
		ids = append(ids, r.RemoteID)
	}
	return ids
}

func TestUpcomingCalendarRemindersSelectsOnlyAnnounceableOccurrences(t *testing.T) {
	db := newCalendarReminderDB(t)
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	cancelled := timedReminderEvent("cancelled", now.Add(5*time.Minute))
	cancelled.Status = "cancelled"
	declined := timedReminderEvent("declined", now.Add(5*time.Minute))
	declined.ResponseStatus = "declined"
	organizer := timedReminderEvent("organizer", now.Add(6*time.Minute))
	organizer.ResponseStatus = "organizer"
	replaceReminderEvents(t, db, now,
		timedReminderEvent("soon", now.Add(10*time.Minute)),
		timedReminderEvent("at-the-edge", now.Add(15*time.Minute)),
		timedReminderEvent("later", now.Add(16*time.Minute)),
		timedReminderEvent("started", now.Add(-time.Minute)),
		timedReminderEvent("starting-now", now),
		cancelled, declined, organizer,
		CalendarEvent{RemoteID: "all-day", Summary: "Holiday", AllDay: true, StartDate: "2026-10-20", EndDate: "2026-10-21"},
	)
	got := reminderRemoteIDs(t, db, now)
	want := []string{"organizer", "soon", "at-the-edge"}
	if len(got) != len(want) {
		t.Fatalf("reminders = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reminders = %v, want %v", got, want)
		}
	}

	for _, hide := range []string{
		`UPDATE calendar_sources SET is_hidden = 1`,
		`UPDATE calendar_sources SET is_selected = 0`,
		`UPDATE accounts SET is_deleting = 1`,
	} {
		if _, err := db.Write().Exec(hide); err != nil {
			t.Fatal(err)
		}
		if got := reminderRemoteIDs(t, db, now); len(got) != 0 {
			t.Fatalf("after %q reminders = %v, want none", hide, got)
		}
		if _, err := db.Write().Exec(`UPDATE calendar_sources SET is_hidden = 0, is_selected = 1; UPDATE accounts SET is_deleting = 0`); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCalendarReminderIsClaimedOncePerOccurrenceAcrossResyncAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raven.db")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().Exec(`
		INSERT INTO users (id, username, username_normalized) VALUES ('rem-user', 'rem-user', 'rem-user');
		INSERT INTO accounts (id, user_id, provider, email_address) VALUES ('rem-account', 'rem-user', 'gmail', 'rem@example.com');`); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceCalendarSources(t.Context(), "rem-user", "rem-account", "gmail", []CalendarSource{{ID: "rem-source", RemoteID: "primary", Name: "Work", IsSelected: true}}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	start := now.Add(8 * time.Minute)
	replaceReminderEvents(t, db, now, timedReminderEvent("standup", start))

	reminders, err := db.UpcomingCalendarReminders(t.Context(), now, 15*time.Minute, 10)
	if err != nil || len(reminders) != 1 || !reminders[0].StartAt.Equal(start) || reminders[0].Summary != "standup" || reminders[0].Location != "Room 1" {
		t.Fatalf("reminders = %#v, %v", reminders, err)
	}
	if claimed, err := db.ClaimCalendarReminder(t.Context(), reminders[0], now); err != nil || !claimed {
		t.Fatalf("first claim = %v, %v", claimed, err)
	}
	if claimed, err := db.ClaimCalendarReminder(t.Context(), reminders[0], now); err != nil || claimed {
		t.Fatalf("second claim = %v, %v; want false", claimed, err)
	}
	if got := reminderRemoteIDs(t, db, now); len(got) != 0 {
		t.Fatalf("announced occurrence listed again: %v", got)
	}

	// A re-sync rewrites the cached row; the record must survive it.
	replaceReminderEvents(t, db, now, timedReminderEvent("standup", start))
	if got := reminderRemoteIDs(t, db, now); len(got) != 0 {
		t.Fatalf("after re-sync: %v", got)
	}
	// A server restart reopens the database; the record is on disk.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := reminderRemoteIDs(t, db, now); len(got) != 0 {
		t.Fatalf("after reopen: %v", got)
	}

	// Moving the meeting is a new occurrence time and announces again.
	moved := now.Add(12 * time.Minute)
	replaceReminderEvents(t, db, now, timedReminderEvent("standup", moved))
	if got := reminderRemoteIDs(t, db, now); len(got) != 1 || got[0] != "standup" {
		t.Fatalf("after reschedule: %v", got)
	}
}

func TestPruneCalendarRemindersDropsOnlyPastOccurrences(t *testing.T) {
	db := newCalendarReminderDB(t)
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	for _, start := range []time.Time{now.Add(-48 * time.Hour), now.Add(10 * time.Minute)} {
		if _, err := db.ClaimCalendarReminder(t.Context(), CalendarReminder{SourceID: "rem-source", RemoteID: "r", StartAt: start}, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.PruneCalendarReminders(t.Context(), now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Read().QueryRow(`SELECT COUNT(*) FROM calendar_reminders`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("records after prune = %d, %v; want 1", n, err)
	}
}

// A database at v124 has no calendar_reminders; opening it adds the table and
// keeps what was there.
func TestMigrateV124ToV125AddsCalendarReminders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raven.db")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().Exec(`DROP TABLE calendar_reminders; DELETE FROM schema_version; INSERT INTO schema_version (version) VALUES (124)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, tables int
	if err := db.Read().QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil || version != CurrentSchemaVersion {
		t.Fatalf("version = %d, %v; want %d", version, err, CurrentSchemaVersion)
	}
	if err := db.Read().QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name = 'calendar_reminders'`).Scan(&tables); err != nil || tables != 1 {
		t.Fatalf("calendar_reminders tables = %d, %v", tables, err)
	}
}
