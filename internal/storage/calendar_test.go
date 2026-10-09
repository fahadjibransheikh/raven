package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

func TestMigrateV96ToV97CreatesCalendarTablesAndIsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gofer.db")
	raw, err := openDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP);
		INSERT INTO schema_version (version) VALUES (96);
		CREATE TABLE accounts (id TEXT PRIMARY KEY, email_address TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	db, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var version int
	if err := db.Read().QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil || version != 104 {
		t.Fatalf("version = %d, %v; want 104", version, err)
	}
	for _, table := range []string{"calendars", "calendar_events", "calendar_account_state"} {
		var n int
		if err := db.Read().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("%s missing: %v", table, err)
		}
	}
	tx, _ := db.Write().Begin()
	defer tx.Rollback()
	if err := migrateV96ToV97(tx); err != nil {
		t.Fatalf("second run: %v", err)
	}
}

func newCalendarTestDB(t *testing.T) (*DB, []int64) {
	t.Helper()
	db := newIdentityTestDB(t)
	ctx := context.Background()
	if err := db.UpsertCalendars(ctx, "a1", []models.Calendar{
		{ProviderCalendarID: "primary@x", Name: "Me", Color: "#111111", IsPrimary: true, AccessRole: "owner"},
		{ProviderCalendarID: "holidays", Name: "Holidays", AccessRole: "reader"},
	}); err != nil {
		t.Fatal(err)
	}
	cals, err := db.ListCalendarsForUser(ctx, "u1")
	if err != nil || len(cals) != 2 {
		t.Fatalf("calendars = %+v, %v", cals, err)
	}
	return db, []int64{cals[0].ID, cals[1].ID}
}

func ts(s string) time.Time {
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return v
}

func timedEvent(id, start, end string) models.CalendarEvent {
	return models.CalendarEvent{ProviderEventID: id, Title: id, Status: "confirmed", StartAt: ts(start), EndAt: ts(end)}
}

func allDayEvent(id, startDate, endDate string) models.CalendarEvent {
	s, _ := time.Parse("2006-01-02", startDate)
	e, _ := time.Parse("2006-01-02", endDate)
	return models.CalendarEvent{ProviderEventID: id, Title: id, Status: "confirmed", AllDay: true, StartAt: s, EndAt: e, StartDate: startDate, EndDate: endDate}
}

func titles(evs []models.CalendarEventView) map[string]bool {
	m := map[string]bool{}
	for _, e := range evs {
		m[e.Title] = true
	}
	return m
}

func TestCalendarDefaultSelectionAndOwnerScopedToggle(t *testing.T) {
	db, ids := newCalendarTestDB(t)
	ctx := context.Background()
	cals, _ := db.ListCalendarsForUser(ctx, "u1")
	if !cals[0].Selected || cals[1].Selected {
		t.Fatalf("default selection = %v/%v, want owned selected, reader not", cals[0].Selected, cals[1].Selected)
	}
	if other, _ := db.ListCalendarsForUser(ctx, "u2"); len(other) != 0 {
		t.Fatalf("u2 sees %d calendars", len(other))
	}
	if err := db.SetCalendarSelected(ctx, "u2", ids[0], false); !errors.Is(err, ErrCalendarNotFound) {
		t.Fatalf("foreign toggle err = %v", err)
	}
	if err := db.SetCalendarSelected(ctx, "u1", ids[1], true); err != nil {
		t.Fatal(err)
	}
	// A re-sync of the list must keep the user's choice.
	if err := db.UpsertCalendars(ctx, "a1", []models.Calendar{
		{ProviderCalendarID: "primary@x", Name: "Me2", IsPrimary: true, AccessRole: "owner"},
		{ProviderCalendarID: "holidays", Name: "Holidays", AccessRole: "reader"},
	}); err != nil {
		t.Fatal(err)
	}
	cals, _ = db.ListCalendarsForUser(ctx, "u1")
	if !cals[1].Selected || cals[0].Name != "Me2" {
		t.Fatalf("after re-sync = %+v", cals)
	}
	// Dropping a calendar from the provider list removes it and its events.
	if err := db.ApplyCalendarEventPage(ctx, ids[1], "a1", []models.CalendarEvent{timedEvent("h", "2026-03-01T10:00:00Z", "2026-03-01T11:00:00Z")}, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertCalendars(ctx, "a1", []models.Calendar{{ProviderCalendarID: "primary@x", IsPrimary: true, AccessRole: "owner"}}); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.Read().QueryRow(`SELECT COUNT(*) FROM calendar_events`).Scan(&n)
	if n != 0 {
		t.Fatalf("events after calendar removal = %d", n)
	}
}

func TestListCalendarEventsOverlap(t *testing.T) {
	db, ids := newCalendarTestDB(t)
	ctx := context.Background()
	if err := db.ApplyCalendarEventPage(ctx, ids[0], "a1", []models.CalendarEvent{
		timedEvent("inside", "2026-03-10T10:00:00Z", "2026-03-10T11:00:00Z"),
		timedEvent("spans-start", "2026-03-09T23:00:00Z", "2026-03-10T01:00:00Z"),
		timedEvent("spans-end", "2026-03-10T23:00:00Z", "2026-03-11T02:00:00Z"),
		timedEvent("spans-all", "2026-03-09T00:00:00Z", "2026-03-12T00:00:00Z"),
		timedEvent("ends-at-start", "2026-03-09T22:00:00Z", "2026-03-10T00:00:00Z"),
		timedEvent("starts-at-end", "2026-03-11T00:00:00Z", "2026-03-11T01:00:00Z"),
		timedEvent("zero-at-start", "2026-03-10T00:00:00Z", "2026-03-10T00:00:00Z"),
		timedEvent("fractional", "2026-03-10T05:00:00.5Z", "2026-03-10T05:30:00Z"),
	}, nil); err != nil {
		t.Fatal(err)
	}
	// Unselected calendar events are hidden.
	_ = db.ApplyCalendarEventPage(ctx, ids[1], "a1", []models.CalendarEvent{timedEvent("hidden", "2026-03-10T10:00:00Z", "2026-03-10T11:00:00Z")}, nil)

	got, err := db.ListCalendarEvents(ctx, "u1", ts("2026-03-10T00:00:00Z"), ts("2026-03-11T00:00:00Z"), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"inside": true, "spans-start": true, "spans-end": true, "spans-all": true, "zero-at-start": true, "fractional": true}
	if g := titles(got); len(g) != len(want) {
		t.Fatalf("got %v, want %v", g, want)
	} else {
		for k := range want {
			if !g[k] {
				t.Fatalf("missing %s in %v", k, g)
			}
		}
	}
	if got, _ := db.ListCalendarEvents(ctx, "u2", ts("2026-03-10T00:00:00Z"), ts("2026-03-11T00:00:00Z"), time.UTC); len(got) != 0 {
		t.Fatalf("foreign user sees %d events", len(got))
	}
}

func TestListCalendarEventsAllDayUsesLocalDates(t *testing.T) {
	db, ids := newCalendarTestDB(t)
	ctx := context.Background()
	if err := db.ApplyCalendarEventPage(ctx, ids[0], "a1", []models.CalendarEvent{
		allDayEvent("mar4", "2026-03-04", "2026-03-05"),
		allDayEvent("mar5", "2026-03-05", "2026-03-06"),
		allDayEvent("mar6", "2026-03-06", "2026-03-07"),
		allDayEvent("mar5-7", "2026-03-05", "2026-03-08"),
	}, nil); err != nil {
		t.Fatal(err)
	}
	la, _ := time.LoadLocation("America/Los_Angeles")
	// The Los Angeles day of 2026-03-05: [08:00Z, next 08:00Z). A UTC-instant
	// comparison would wrongly include mar6 (starts 00:00Z < 08:00Z next day).
	day := time.Date(2026, 3, 5, 0, 0, 0, 0, la)
	got, err := db.ListCalendarEvents(ctx, "u1", day, day.AddDate(0, 0, 1), la)
	if err != nil {
		t.Fatal(err)
	}
	g := titles(got)
	if len(g) != 2 || !g["mar5"] || !g["mar5-7"] {
		t.Fatalf("LA day = %v, want mar5 and mar5-7", g)
	}
	// Mid-day end still covers that final local day.
	got, _ = db.ListCalendarEvents(ctx, "u1", day, day.Add(36*time.Hour), la)
	if g := titles(got); !g["mar6"] || g["mar4"] {
		t.Fatalf("36h range = %v", g)
	}
}

func TestReplaceAndIncrementalPrune(t *testing.T) {
	db, ids := newCalendarTestDB(t)
	ctx := context.Background()
	ws, we := ts("2026-01-01T00:00:00Z"), ts("2026-12-31T00:00:00Z")
	if err := db.ReplaceCalendarEvents(ctx, ids[0], "a1", []models.CalendarEvent{timedEvent("old", "2026-02-01T10:00:00Z", "2026-02-01T11:00:00Z")}, "tok1", ws, we); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceCalendarEvents(ctx, ids[0], "a1", []models.CalendarEvent{timedEvent("new", "2026-02-02T10:00:00Z", "2026-02-02T11:00:00Z")}, "tok2", ws, we); err != nil {
		t.Fatal(err)
	}
	// Incremental page brings a past and a far-future event; both get pruned.
	if err := db.ApplyCalendarEventPage(ctx, ids[0], "a1", []models.CalendarEvent{
		timedEvent("ancient", "2025-06-01T10:00:00Z", "2025-06-01T11:00:00Z"),
		timedEvent("far", "2027-06-01T10:00:00Z", "2027-06-01T11:00:00Z"),
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishIncrementalCalendarSync(ctx, ids[0], "tok3"); err != nil {
		t.Fatal(err)
	}
	var n int
	var tok string
	_ = db.Read().QueryRow(`SELECT COUNT(*) FROM calendar_events`).Scan(&n)
	_ = db.Read().QueryRow(`SELECT sync_token FROM calendars WHERE id = ?`, ids[0]).Scan(&tok)
	if n != 1 || tok != "tok3" {
		t.Fatalf("events=%d token=%q, want 1 event and tok3", n, tok)
	}
}
