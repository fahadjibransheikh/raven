package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

func TestFindCalendarEventByICalUIDOwnerScopedAndPrefersSeries(t *testing.T) {
	db, ids := newCalendarTestDB(t)
	ctx := context.Background()
	mk := func(id, uid, rec, start string) models.CalendarEvent {
		e := timedEvent(id, start, start)
		e.ICalUID, e.RecurringEventID = uid, rec
		return e
	}
	if err := db.ApplyCalendarEventPage(ctx, ids[0], "a1", []models.CalendarEvent{
		mk("inst2", "series@x", "master", "2026-03-17T10:00:00Z"),
		mk("inst1", "series@x", "master", "2026-03-10T10:00:00Z"),
		mk("solo", "solo@x", "", "2026-03-11T10:00:00Z"),
	}, nil); err != nil {
		t.Fatal(err)
	}
	// An unselected calendar of the account still counts.
	if err := db.ApplyCalendarEventPage(ctx, ids[1], "a1", []models.CalendarEvent{mk("hol", "hol@x", "", "2026-03-12T10:00:00Z")}, nil); err != nil {
		t.Fatal(err)
	}

	if got, err := db.FindCalendarEventByICalUID(ctx, "u1", "a1", "solo@x"); err != nil || got.ProviderEventID != "solo" || got.ID == 0 {
		t.Fatalf("solo = %+v, %v", got, err)
	}
	if got, err := db.FindCalendarEventByICalUID(ctx, "u1", "a1", "series@x"); err != nil || got.ProviderEventID != "inst1" {
		t.Fatalf("series with no master should give the first instance, got %+v, %v", got, err)
	}
	master := mk("master", "series@x", "", "2026-03-03T10:00:00Z")
	if err := db.ApplyCalendarEventPage(ctx, ids[0], "a1", []models.CalendarEvent{master}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.FindCalendarEventByICalUID(ctx, "u1", "a1", "series@x"); got.ProviderEventID != "master" {
		t.Fatalf("master should win, got %q", got.ProviderEventID)
	}
	if _, err := db.FindCalendarEventByICalUID(ctx, "u1", "a1", "hol@x"); err != nil {
		t.Fatalf("unselected calendar: %v", err)
	}
	for name, args := range map[string][3]string{
		"foreign user":  {"u2", "a1", "solo@x"},
		"wrong account": {"u1", "nope", "solo@x"},
		"unknown uid":   {"u1", "a1", "missing@x"},
		"empty uid":     {"u1", "a1", ""},
	} {
		if _, err := db.FindCalendarEventByICalUID(ctx, args[0], args[1], args[2]); !errors.Is(err, ErrCalendarNotFound) {
			t.Errorf("%s: err = %v, want ErrCalendarNotFound", name, err)
		}
	}
}
