package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/fahadjibransheikh/raven/internal/mail"
	"github.com/fahadjibransheikh/raven/internal/storage"
)

func seedOwnerMeeting(t *testing.T, f *userStorageFixture, owner, summary string, start time.Time) {
	t.Helper()
	end := start.Add(time.Hour)
	if err := f.routing.WithUser(t.Context(), owner, func(db *storage.DB) error {
		if err := db.ReplaceCalendarSources(t.Context(), owner, f.accounts[owner].ID, storage.CalendarSourceProviderCalDAV, []storage.CalendarSource{
			{ID: owner + "-calendar", RemoteID: "work", Name: "Work", IsSelected: true},
		}); err != nil {
			return err
		}
		return db.ReplaceCalendarEvents(t.Context(), owner, owner+"-calendar", []storage.CalendarEvent{
			{RemoteID: "meeting", Summary: summary, StartAt: &start, EndAt: &end},
		}, start.Add(-24*time.Hour), start.Add(48*time.Hour))
	}); err != nil {
		t.Fatal(err)
	}
}

// The managed-mode worker finds a meeting about to start in the owner's own
// store and announces it only to her; her Web Push subscriptions get it, his do not.
func TestUserStorageCalendarReminderIsAnnouncedToItsOwnerOnly(t *testing.T) {
	f := newUserStorageFixtureMode(t, true)
	seedOwnerMeeting(t, f, "alice", "Roadmap review", time.Now().UTC().Add(5*time.Minute))
	f.subscribe(t, "alice", "https://push.example/alice")
	f.subscribe(t, "bob", "https://push.example/bob")

	events := f.events.Subscribe()
	defer f.events.Unsubscribe(events)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.base.StartMessageTimerWorker(ctx) // runs a pass at once

	var reminder mail.Event
	select {
	case reminder = <-events:
	case <-time.After(10 * time.Second):
		t.Fatal("no calendar reminder")
	}
	if reminder.Type != mail.EventCalendarReminder || reminder.UserID != "alice" || reminder.AccountID != f.accounts["alice"].ID || reminder.Payload["title"] != "Roadmap review" {
		t.Fatalf("reminder = %#v", reminder)
	}

	var delivered []string
	var pushed map[string]any
	var options *webpush.Options
	f.service.sendNotification = func(_ context.Context, body []byte, sub *webpush.Subscription, o *webpush.Options) (*http.Response, error) {
		delivered = append(delivered, sub.Endpoint)
		options = o
		_ = json.Unmarshal(body, &pushed)
		return pushResponse(http.StatusCreated), nil
	}
	f.service.handleCalendarReminder(t.Context(), reminder)
	if len(delivered) != 1 || delivered[0] != "https://push.example/alice" {
		t.Fatalf("push went to %v", delivered)
	}
	if pushed["title"] != "Roadmap review" || pushed["url"] != "/calendar" || pushed["tag"] != "raven-calendar-"+reminder.Payload["event_id"].(string) {
		t.Fatalf("pushed = %v", pushed)
	}
	if options.TTL != calendarReminderPushTTL {
		t.Fatalf("TTL = %d, want %d", options.TTL, calendarReminderPushTTL)
	}

	// A pass a moment later does not announce the same meeting again.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Type == mail.EventCalendarReminder {
				t.Fatalf("announced twice: %#v", e)
			}
		case <-deadline:
			return
		}
	}
}

func TestCalendarReminderPushFollowsTheNotificationSettings(t *testing.T) {
	f := newUserStorageFixture(t)
	f.subscribe(t, "alice", "https://push.example/alice")
	pushes := 0
	f.service.sendNotification = func(_ context.Context, _ []byte, _ *webpush.Subscription, _ *webpush.Options) (*http.Response, error) {
		pushes++
		return pushResponse(http.StatusCreated), nil
	}
	reminder := mail.Event{Type: mail.EventCalendarReminder, UserID: "alice", AccountID: f.accounts["alice"].ID, Payload: map[string]any{"event_id": "e1", "title": "Sync", "body": "Starts in 5 minutes", "url": "/calendar"}}
	set := func(settings map[string]string) {
		t.Helper()
		if err := f.routing.WithUser(t.Context(), "alice", func(db *storage.DB) error {
			return db.MergeUISettings(t.Context(), "alice", settings)
		}); err != nil {
			t.Fatal(err)
		}
	}

	f.service.handleCalendarReminder(t.Context(), reminder)
	if pushes != 1 {
		t.Fatalf("pushes with web push on = %d, want 1", pushes)
	}
	for _, off := range []map[string]string{{"desktop_notifications": "false"}, {"desktop_notifications": "true", "notification_mode": "browser_tab"}, {"notification_mode": "off"}} {
		set(map[string]string{"desktop_notifications": "true", "notification_mode": "web_push"})
		set(off)
		before := pushes
		f.service.handleCalendarReminder(t.Context(), reminder)
		if pushes != before {
			t.Fatalf("pushed with settings %v", off)
		}
	}

	// An event that names someone else's account is not delivered to the claimed user.
	set(map[string]string{"desktop_notifications": "true", "notification_mode": "web_push"})
	before := pushes
	f.service.handleCalendarReminder(t.Context(), mail.Event{Type: mail.EventCalendarReminder, UserID: "bob", AccountID: f.accounts["alice"].ID, Payload: reminder.Payload})
	if pushes != before {
		t.Fatal("pushed for a user who does not own the account")
	}
}
