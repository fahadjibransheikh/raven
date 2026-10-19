package handler

import (
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/mail"
	"github.com/fahadjibransheikh/raven/internal/storage"
)

func seedReminderEvents(t *testing.T, db *storage.DB, now time.Time, events ...storage.CalendarEvent) {
	t.Helper()
	if err := db.ReplaceCalendarSources(t.Context(), "owner", "victim-account", storage.CalendarSourceProviderCalDAV, []storage.CalendarSource{
		{ID: "owner-calendar", RemoteID: "work", Name: "Work", IsSelected: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceCalendarEvents(t.Context(), "owner", "owner-calendar", events, now.Add(-24*time.Hour), now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func reminderEvent(remoteID, summary string, start time.Time) storage.CalendarEvent {
	end := start.Add(time.Hour)
	return storage.CalendarEvent{RemoteID: remoteID, Summary: summary, Location: "Room 4", StartAt: &start, EndAt: &end}
}

func collectReminders(t *testing.T, db *storage.DB, now time.Time) []mail.Event {
	t.Helper()
	var events []mail.Event
	runCalendarRemindersIn(t.Context(), db, now, func(e mail.Event) { events = append(events, e) })
	for _, e := range events {
		if e.Type != mail.EventCalendarReminder || e.UserID != "owner" || e.AccountID != "victim-account" {
			t.Fatalf("reminder event = %#v", e)
		}
	}
	return events
}

func TestCalendarRemindersAnnounceEachOccurrenceOnceInsideTheLeadTime(t *testing.T) {
	_, db := newAccountOwnershipTestHandler(t)
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	declined := reminderEvent("declined", "Declined sync", now.Add(5*time.Minute))
	declined.ResponseStatus = "declined"
	seedReminderEvents(t, db, now,
		reminderEvent("planning", "Planning", now.Add(14*time.Minute)),
		reminderEvent("started", "Already started", now.Add(-2*time.Minute)),
		declined,
		storage.CalendarEvent{RemoteID: "holiday", Summary: "Holiday", AllDay: true, StartDate: "2026-10-20", EndDate: "2026-10-21"},
	)

	// Default lead time is 10 minutes: Planning is 14 minutes away.
	if events := collectReminders(t, db, now); len(events) != 0 {
		t.Fatalf("announced before the lead time: %#v", events)
	}
	now = now.Add(5*time.Minute + 20*time.Second)
	events := collectReminders(t, db, now)
	if len(events) != 1 {
		t.Fatalf("events = %#v, want only Planning", events)
	}
	payload := events[0].Payload
	if payload["title"] != "Planning" || payload["body"] != "Starts in 9 minutes · Room 4" || payload["url"] != "/calendar" || payload["start_at"] != "2026-10-20T09:14:00Z" || payload["event_id"] == "" {
		t.Fatalf("payload = %#v", payload)
	}
	// The next ticks, a re-sync and a restart (a new pass over the same store) stay quiet.
	if again := collectReminders(t, db, now.Add(30*time.Second)); len(again) != 0 {
		t.Fatalf("announced twice: %#v", again)
	}
	seedReminderEvents(t, db, now, reminderEvent("planning", "Planning", time.Date(2026, 10, 20, 9, 14, 0, 0, time.UTC)))
	if again := collectReminders(t, db, now.Add(time.Minute)); len(again) != 0 {
		t.Fatalf("announced again after a re-sync: %#v", again)
	}
}

func TestCalendarReminderLeadSettingControlsAndClamps(t *testing.T) {
	_, db := newAccountOwnershipTestHandler(t)
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	seedReminderEvents(t, db, now, reminderEvent("review", "Review", now.Add(12*time.Minute)))
	set := func(value string) {
		t.Helper()
		if err := db.MergeUISettings(t.Context(), "owner", map[string]string{"calendar_reminder_minutes": value}); err != nil {
			t.Fatal(err)
		}
	}

	set("0")
	if events := collectReminders(t, db, now.Add(11*time.Minute)); len(events) != 0 {
		t.Fatalf("announced while off: %#v", events)
	}
	set("5")
	if events := collectReminders(t, db, now.Add(6*time.Minute)); len(events) != 0 {
		t.Fatalf("announced 6 minutes out with a 5 minute lead: %#v", events)
	}
	// Turning it back on announces what is still ahead.
	set("15")
	if events := collectReminders(t, db, now.Add(6*time.Minute)); len(events) != 1 || events[0].Payload["body"] != "Starts in 6 minutes · Room 4" {
		t.Fatalf("events with a 15 minute lead = %#v", events)
	}

	// An unknown stored value means the 10 minute default, not "off" or "forever".
	for value, want := range map[string]time.Duration{"": 10 * time.Minute, "999": 10 * time.Minute, "-5": 10 * time.Minute, "abc": 10 * time.Minute, "0": 0, "5": 5 * time.Minute, "10": 10 * time.Minute, "15": 15 * time.Minute} {
		if got := calendarReminderLead(map[string]string{"calendar_reminder_minutes": value}); got != want {
			t.Errorf("lead for %q = %v, want %v", value, got, want)
		}
	}
}

func TestCalendarRemindersStayQuietForEventsThatStartedWhileTheServerWasDown(t *testing.T) {
	_, db := newAccountOwnershipTestHandler(t)
	start := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	seedReminderEvents(t, db, start, reminderEvent("standup", "Standup", start))
	// The server comes back up after the meeting began.
	if events := collectReminders(t, db, start.Add(time.Minute)); len(events) != 0 {
		t.Fatalf("announced a meeting that already started: %#v", events)
	}
	// Starting right now still counts as about to start.
	if events := collectReminders(t, db, start.Add(-20*time.Second)); len(events) != 1 || events[0].Payload["body"] != "Starting now · Room 4" {
		t.Fatalf("events = %#v", events)
	}
}
