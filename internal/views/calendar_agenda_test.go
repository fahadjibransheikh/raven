package views

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// agendaScenario is a week view of 2026-10-19 (Mon) with "today" set to the 19th:
// a head-office all-day entry repeated on three days, one all-day entry on a single day,
// two timed events today (one over, one later), a pending invite tomorrow, and an event
// on another calendar.
func agendaScenario(t *testing.T, secondCalendar bool) (CalendarMonthData, string) {
	t.Helper()
	utc := time.UTC
	data := NewCalendarTimelineData(time.Date(2026, time.October, 19, 12, 0, 0, 0, utc), "week")
	data.TodayDateKey = "2026-10-19"
	at := func(day, hour, minute int) *time.Time {
		v := time.Date(2026, time.October, day, hour, minute, 0, 0, utc)
		return &v
	}
	office := func(id, start, end string) CalendarEvent {
		return CalendarEvent{ID: id, SourceID: "work", SourceName: "me@example.com", Summary: "Karachi Head Office (Office)", AllDay: true, StartDate: start, EndDate: end}
	}
	data.Events = []CalendarEvent{
		office("o1", "2026-10-19", "2026-10-20"),
		office("o2", "2026-10-20", "2026-10-21"),
		office("o3", "2026-10-21", "2026-10-22"),
		{ID: "standup", SourceID: "work", SourceName: "me@example.com", Summary: "Standup", StartAt: at(19, 9, 0), EndAt: at(19, 9, 30)},
		{ID: "review", SourceID: "work", SourceName: "me@example.com", Summary: "Design review", Location: "Room 4", StartAt: at(19, 14, 0), EndAt: at(19, 15, 0)},
		{ID: "invite", SourceID: "work", SourceName: "me@example.com", Summary: "Planning", ResponseStatus: "needsAction", StartAt: at(20, 10, 0), EndAt: at(20, 11, 0)},
		{ID: "lunch", SourceID: "work", SourceName: "me@example.com", Summary: "Lunch", StartAt: at(21, 12, 0), EndAt: at(21, 13, 0)},
		{ID: "dentist", SourceID: "work", SourceName: "me@example.com", Summary: "Dentist", AllDay: true, StartDate: "2026-10-22", EndDate: "2026-10-23"},
	}
	if secondCalendar {
		data.Events = append(data.Events, CalendarEvent{ID: "gym", SourceID: "home", SourceName: "Home", Summary: "Gym", StartAt: at(21, 18, 0), EndAt: at(21, 19, 0)})
	}
	var out bytes.Buffer
	if err := CalendarAgendaPane(data).Render(t.Context(), &out); err != nil {
		t.Fatal(err)
	}
	return data, out.String()
}

func TestCalendarAgendaGroupsByDayWithRelativeHeadings(t *testing.T) {
	data, html := agendaScenario(t, false)
	days := calendarAgendaGroups(data).Days
	var labels []string
	for _, day := range days {
		labels = append(labels, day.Label)
	}
	if got, want := strings.Join(labels, "|"), "Today|Tomorrow|Wed, Oct 21|Thu, Oct 22"; got != want {
		t.Fatalf("day headings = %q, want %q", got, want)
	}
	if strings.Count(html, "data-calendar-agenda-day-heading") != 4 {
		t.Fatal("each day needs one heading")
	}
	// The per-card date is gone: "Mon, Oct 19" appears nowhere (Today replaces it).
	if strings.Contains(html, "Mon, Oct 19") || strings.Contains(html, "text-primary\">Tue") {
		t.Fatal("rows must not repeat the date")
	}
	today := days[0].Rows
	if len(today) != 3 || today[0].TimeLabel != "All day" || today[1].TimeLabel != "09:00–09:30" || today[2].TimeLabel != "14:00–15:00" {
		t.Fatalf("today's rows = %+v, want the all-day span, then standup (09:00), then review (14:00)", today)
	}
}

func TestCalendarAgendaCollapsesRepeatedAllDayEventToOneSpanRow(t *testing.T) {
	data, html := agendaScenario(t, false)
	if got := strings.Count(html, "Karachi Head Office (Office)"); got != 2 { // visible title + aria-label
		t.Fatalf("head office title count = %d, want 1 row (title + aria-label)", got)
	}
	row := calendarAgendaGroups(data).Days[0].Rows[0]
	if row.SpanLabel != "Oct 19–21" || row.StartDate != "2026-10-19" || row.EndDate != "2026-10-22" {
		t.Fatalf("merged row = %+v, want Oct 19–21 over 2026-10-19..2026-10-22", row)
	}
	// Day selection matches on the merged span, so the row still shows on the 20th and 21st.
	if !strings.Contains(html, `data-calendar-event-start-date="2026-10-19" data-calendar-event-end-date="2026-10-22"`) {
		t.Fatal("merged row must carry the whole span for day selection")
	}
	if !strings.Contains(html, `aria-label="Open event: Karachi Head Office (Office) · Oct 19–21"`) {
		t.Fatal("span must be part of the accessible name")
	}
	// A lone all-day event has no span label, and different titles never merge.
	if strings.Contains(html, "Oct 22–22") || strings.Count(html, "Dentist") != 2 {
		t.Fatal("single-day all-day event should stay a plain row")
	}
}

func TestCalendarAgendaDoesNotMergeAcrossGapsOrCalendars(t *testing.T) {
	data, _ := agendaScenario(t, false)
	e := func(id, source, start, end string) CalendarEvent {
		return CalendarEvent{ID: id, SourceID: source, Summary: "Out", AllDay: true, StartDate: start, EndDate: end}
	}
	data.Events = []CalendarEvent{e("a", "s1", "2026-10-19", "2026-10-20"), e("b", "s1", "2026-10-21", "2026-10-22"), e("c", "s2", "2026-10-19", "2026-10-20")}
	rows := 0
	for _, day := range calendarAgendaGroups(data).Days {
		rows += len(day.Rows)
	}
	if rows != 3 {
		t.Fatalf("rows = %d, want 3 (a gap and another calendar both break the run)", rows)
	}
}

func TestCalendarAgendaRowsAreCompactAndKeepTheirTriggers(t *testing.T) {
	_, html := agendaScenario(t, false)
	for _, gone := range []string{"rounded-xl", "shadow-sm", "bg-card", "size-9"} {
		if strings.Contains(html, gone) {
			t.Errorf("agenda still has card styling %q", gone)
		}
	}
	for _, want := range []string{`data-calendar-agenda-time`, `hx-get="/api/calendar/events/review"`, `aria-haspopup="dialog"`, `data-calendar-event-trigger`, "Room 4"} {
		if !strings.Contains(html, want) {
			t.Errorf("agenda missing %q", want)
		}
	}
	if strings.Count(html, "<button") != strings.Count(html, "data-calendar-agenda-event")+countBackButtons(html) {
		t.Error("every agenda row must be a single button")
	}
}

func countBackButtons(html string) int {
	return strings.Count(html, "data-calendar-agenda-back") + strings.Count(html, "data-calendar-agenda-clear")
}

func TestCalendarAgendaShowsAccountOnlyWithSeveralCalendars(t *testing.T) {
	_, one := agendaScenario(t, false)
	if strings.Contains(one, "me@example.com") {
		t.Fatal("one calendar in view: the account line must not repeat on every row")
	}
	_, two := agendaScenario(t, true)
	if !strings.Contains(two, "me@example.com") || !strings.Contains(two, ">Home<") {
		t.Fatal("several calendars in view: rows must name their calendar")
	}
}

func TestCalendarAgendaAwaitingResponseIsQuietText(t *testing.T) {
	data, html := agendaScenario(t, false)
	if !strings.Contains(html, `data-calendar-response-state="pending"`) || !strings.Contains(html, ">Awaiting response</span>") {
		t.Fatal("pending invite must keep its label")
	}
	for _, loud := range []string{"badge", "font-bold leading-none"} {
		if strings.Contains(html, loud) {
			t.Fatalf("pending label is still a pill (%q)", loud)
		}
	}
	for _, day := range calendarAgendaGroups(data).Days {
		for _, row := range day.Rows {
			if row.Event.ID != "invite" && calendarAgendaResponseVisible(row.Event) {
				t.Fatalf("%s has no reply status to show", row.Event.ID)
			}
		}
	}
}

func TestCalendarAgendaPutsEventsBeforeTheViewOnItsFirstDay(t *testing.T) {
	data, _ := agendaScenario(t, false)
	data.Events = []CalendarEvent{{ID: "long", SourceID: "s", Summary: "Trip", AllDay: true, StartDate: "2026-10-10", EndDate: "2026-10-21"}}
	days := calendarAgendaGroups(data).Days
	if len(days) != 1 || days[0].Key != "2026-10-19" || days[0].Label != "Today" {
		t.Fatalf("days = %+v, want the trip listed under the view's first day", days)
	}
}
