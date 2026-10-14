package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fahadjibransheikh/raven/internal/storage"
	"github.com/fahadjibransheikh/raven/internal/views"
)

// The macOS desktop app points RAVEN_WIDGET_SNAPSHOT_PATH into its widget
// extension's sandbox container. The widget only reads this file: it holds no
// token and never talks to the server.
const calendarWidgetSnapshotEnv = "RAVEN_WIDGET_SNAPSHOT_PATH"

const calendarWidgetDays = 3

type calendarWidgetSnapshot struct {
	GeneratedAt string                `json:"generatedAt"`
	Timezone    string                `json:"timezone"`
	Events      []calendarWidgetEvent `json:"events"`
}

// Timed events use RFC3339 Start/End; all-day events use StartDate/EndDate
// (YYYY-MM-DD, end exclusive) so they never shift with the viewer's zone.
type calendarWidgetEvent struct {
	ID             string `json:"id"`
	Summary        string `json:"summary"`
	Location       string `json:"location,omitempty"`
	Color          string `json:"color,omitempty"`
	ResponseStatus string `json:"responseStatus,omitempty"`
	AllDay         bool   `json:"allDay"`
	Start          string `json:"start,omitempty"`
	End            string `json:"end,omitempty"`
	StartDate      string `json:"startDate,omitempty"`
	EndDate        string `json:"endDate,omitempty"`
}

// calendarWidgetWindow is today plus the next two days in the calendar's zone,
// matching the default 3 Days view.
func calendarWidgetWindow(now time.Time, location *time.Location) (time.Time, time.Time) {
	now = now.In(location)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	return start, start.AddDate(0, 0, calendarWidgetDays)
}

func buildCalendarWidgetSnapshot(events []views.CalendarEvent, now time.Time, location *time.Location) ([]byte, error) {
	start, end := calendarWidgetWindow(now, location)
	firstDate, endDate := start.Format("2006-01-02"), end.Format("2006-01-02")
	timezone := location.String()
	if timezone == "Local" {
		timezone = ""
	}
	snapshot := calendarWidgetSnapshot{GeneratedAt: now.UTC().Format(time.RFC3339), Timezone: timezone, Events: []calendarWidgetEvent{}}
	for _, event := range events {
		if event.SourceHidden || strings.EqualFold(event.Status, "cancelled") {
			continue
		}
		item := calendarWidgetEvent{
			ID: event.ID, Summary: strings.TrimSpace(event.Summary), Location: strings.TrimSpace(event.Location),
			Color: views.CalendarEventHexColor(event.SourceColor), ResponseStatus: event.ResponseStatus, AllDay: event.AllDay,
		}
		if event.AllDay {
			if event.StartDate == "" || event.EndDate == "" || event.StartDate >= endDate || event.EndDate <= firstDate {
				continue
			}
			item.StartDate, item.EndDate = event.StartDate, event.EndDate
		} else {
			if event.StartAt == nil || event.EndAt == nil || !event.StartAt.Before(end) || event.EndAt.Before(start) {
				continue
			}
			item.Start, item.End = event.StartAt.UTC().Format(time.RFC3339), event.EndAt.UTC().Format(time.RFC3339)
		}
		snapshot.Events = append(snapshot.Events, item)
	}
	sort.SliceStable(snapshot.Events, func(i, j int) bool {
		return calendarWidgetSortKey(snapshot.Events[i]) < calendarWidgetSortKey(snapshot.Events[j])
	})
	return json.Marshal(snapshot)
}

// All-day events sort before timed events on the same date.
func calendarWidgetSortKey(event calendarWidgetEvent) string {
	if event.AllDay {
		return event.StartDate + " "
	}
	return event.Start
}

func (h *Handler) writeCalendarWidgetSnapshot(ctx context.Context, now time.Time) {
	path := strings.TrimSpace(os.Getenv(calendarWidgetSnapshotEnv))
	if path == "" || h.auth == nil {
		return
	}
	user := h.auth.GetDefaultUser()
	if user == nil {
		return
	}
	// macOS creates the container when the widget first runs; until the user
	// adds the widget there is nowhere to write, so skip quietly.
	if _, err := os.Stat(filepath.Dir(filepath.Dir(path))); err != nil {
		return
	}
	var payload []byte
	err := h.withUserDB(ctx, user.ID, func(db *storage.DB) error {
		location := viewsCalendarLocation(db.GetUISettings(ctx, user.ID))
		start, end := calendarWidgetWindow(now, location)
		events, err := db.ListCalendarEvents(ctx, user.ID, start, end)
		if err != nil {
			return err
		}
		payload, err = buildCalendarWidgetSnapshot(calendarViewEvents(events), now, location)
		return err
	})
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("calendar widget snapshot: %v", err)
		}
		return
	}
	// generatedAt changes every tick; compare the rest so an idle calendar
	// doesn't rewrite the file every 30 seconds.
	if bytes.Equal(calendarWidgetContent(payload), calendarWidgetContent(h.calendarWidgetLast)) {
		return
	}
	if err := writeFileAtomic(path, payload); err != nil {
		log.Printf("calendar widget snapshot: %v", err)
		return
	}
	h.calendarWidgetLast = payload
}

func calendarWidgetContent(payload []byte) []byte {
	if index := bytes.Index(payload, []byte(`"timezone"`)); index >= 0 {
		return payload[index:]
	}
	return payload
}

// The widget may read while we write, so it must only ever see a whole file.
func writeFileAtomic(path string, payload []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".agenda-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(payload); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
