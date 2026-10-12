package storage

import (
	"testing"
)

func TestReplaceCalendarSourcesAppliesRavenCalendarCarryoverOnce(t *testing.T) {
	ctx := t.Context()
	db := newContactsTestDB(t)
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, user_id, email_address, provider) VALUES ('acc', 'default', 'me@example.com', 'gmail')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO raven_calendar_carryover (account_id, remote_id, selected) VALUES
		('acc', 'kept-on', 1), ('acc', 'kept-off', 0), ('acc', 'gone', 1)`); err != nil {
		t.Fatal(err)
	}
	waiting, err := db.RavenCalendarCarryoverAccounts(ctx)
	if err != nil || len(waiting) != 1 || waiting[0].AccountID != "acc" || waiting[0].UserID != "default" || waiting[0].Provider != "gmail" {
		t.Fatalf("carryover accounts = %v, %v; want [acc]", waiting, err)
	}

	discovered := []CalendarSource{
		// The provider's default would select the first calendar; Raven's choice wins.
		{RemoteID: "kept-off", Name: "Off", IsSelected: true},
		{RemoteID: "kept-on", Name: "On"},
		{RemoteID: "new", Name: "New", IsSelected: true},
	}
	if err := db.ReplaceCalendarSources(ctx, "default", "acc", "gmail", discovered); err != nil {
		t.Fatal(err)
	}
	selected := map[string]bool{}
	sources, err := db.ListCalendarSourcesForAccount(ctx, "default", "acc")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		selected[source.RemoteID] = source.IsSelected
	}
	want := map[string]bool{"kept-on": true, "kept-off": false, "new": true}
	for remoteID, wantSelected := range want {
		if got, ok := selected[remoteID]; !ok || got != wantSelected {
			t.Fatalf("source %q selected = %v (present %v), want %v; all %v", remoteID, got, ok, wantSelected, selected)
		}
	}
	if waiting, err := db.RavenCalendarCarryoverAccounts(ctx); err != nil || len(waiting) != 0 {
		t.Fatalf("carryover after discovery = %v, %v; want none", waiting, err)
	}

	// Later discoveries keep the user's own choices, not the old selection.
	if err := db.SetCalendarSourceSelection(ctx, "default", "acc", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceCalendarSources(ctx, "default", "acc", "gmail", discovered); err != nil {
		t.Fatal(err)
	}
	sources, err = db.ListCalendarSourcesForAccount(ctx, "default", "acc")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source.IsSelected {
			t.Fatalf("rediscovery reselected %q after the user cleared the selection", source.RemoteID)
		}
	}
}

func TestReplaceCalendarSourcesKeepsCarryoverWhenDiscoveryFindsNothing(t *testing.T) {
	ctx := t.Context()
	db := newContactsTestDB(t)
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, user_id, email_address, provider) VALUES ('acc', 'default', 'me@example.com', 'gmail')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO raven_calendar_carryover (account_id, remote_id, selected) VALUES ('acc', 'cal', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceCalendarSources(ctx, "default", "acc", "gmail", nil); err != nil {
		t.Fatal(err)
	}
	if waiting, err := db.RavenCalendarCarryoverAccounts(ctx); err != nil || len(waiting) != 1 {
		t.Fatalf("carryover after empty discovery = %v, %v; want it kept", waiting, err)
	}
}

func TestRavenCalendarCarryoverKeyMatchesCalDAVURLVariants(t *testing.T) {
	saved := ravenCalendarCarryoverKey("https://p112-caldav.icloud.com/1836625426/calendars/home/")
	for _, discovered := range []string{
		"https://p112-caldav.icloud.com/1836625426/calendars/home/",
		"https://P112-CalDAV.icloud.com:443/1836625426/calendars/home",
	} {
		if got := ravenCalendarCarryoverKey(discovered); got != saved {
			t.Fatalf("key(%q) = %q, want %q", discovered, got, saved)
		}
	}
	if ravenCalendarCarryoverKey("https://p112-caldav.icloud.com/1836625426/calendars/work/") == saved {
		t.Fatal("different calendars share a key")
	}
	// Opaque provider IDs are case-sensitive and untouched.
	if ravenCalendarCarryoverKey("AQMkADAw/A=") != "AQMkADAw/A=" || ravenCalendarCarryoverKey("Family@group.calendar.google.com") == ravenCalendarCarryoverKey("family@group.calendar.google.com") {
		t.Fatal("opaque IDs were normalized")
	}
}
