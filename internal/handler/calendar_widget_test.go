package handler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/views"
)

func TestCalendarWidgetSnapshotKeepsThreeVisibleDays(t *testing.T) {
	location, err := time.LoadLocation("Asia/Karachi")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 14, 21, 30, 0, 0, location)
	at := func(day, hour int) *time.Time { value := time.Date(2026, 10, day, hour, 0, 0, 0, location); return &value }
	events := []views.CalendarEvent{
		{ID: "later", Summary: "Review", StartAt: at(16, 9), EndAt: at(16, 10), SourceColor: "22C55E"},
		{ID: "beyond", Summary: "Too late", StartAt: at(17, 0), EndAt: at(17, 1)},
		{ID: "yesterday", Summary: "Done", StartAt: at(13, 9), EndAt: at(13, 10)},
		{ID: "overnight", Summary: "Flight", StartAt: at(13, 22), EndAt: at(14, 6)},
		{ID: "hidden", Summary: "Hidden", StartAt: at(15, 9), EndAt: at(15, 10), SourceHidden: true},
		{ID: "cancelled", Summary: "Off", StartAt: at(15, 9), EndAt: at(15, 10), Status: "cancelled"},
		{ID: "holiday", Summary: "Holiday", AllDay: true, StartDate: "2026-10-12", EndDate: "2026-10-15"},
		{ID: "old-holiday", Summary: "Over", AllDay: true, StartDate: "2026-10-12", EndDate: "2026-10-14"},
		{ID: "today", Summary: "Standup", StartAt: at(14, 10), EndAt: at(14, 11)},
	}
	payload, err := buildCalendarWidgetSnapshot(events, now, location)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot calendarWidgetSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, event := range snapshot.Events {
		ids = append(ids, event.ID)
	}
	want := []string{"holiday", "overnight", "today", "later"}
	if len(ids) != len(want) {
		t.Fatalf("events = %v, want %v", ids, want)
	}
	for index := range want {
		if ids[index] != want[index] {
			t.Fatalf("events = %v, want %v", ids, want)
		}
	}
	if snapshot.Timezone != "Asia/Karachi" || snapshot.Events[3].Color != "#22c55e" || snapshot.Events[3].Start != "2026-10-16T04:00:00Z" {
		t.Fatalf("unexpected snapshot fields: %s", payload)
	}
	if local, _ := buildCalendarWidgetSnapshot(nil, now, time.Local); string(local) == "" || json.Unmarshal(local, &snapshot) != nil || snapshot.Timezone != "" || snapshot.Events == nil {
		t.Fatalf("local zone must be empty and events an empty list: %s", local)
	}
}

func TestCalendarWidgetSnapshotWaitsForWidgetContainer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Container", "Application Support", "Raven", "agenda.json")
	t.Setenv(calendarWidgetSnapshotEnv, path)
	h := &Handler{}
	h.writeCalendarWidgetSnapshot(t.Context(), time.Now())
	if _, err := os.Stat(filepath.Join(dir, "Container")); !os.IsNotExist(err) {
		t.Fatal("writer must not create the widget container itself")
	}
	if err := writeFileAtomic(path, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != `{}` {
		t.Fatalf("atomic write wrote %q", data)
	}
}
