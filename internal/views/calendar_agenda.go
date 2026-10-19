package views

import (
	"sort"
	"strings"
	"time"
)

// calendarAgendaRow is one line of the agenda. Repeated all-day events (the same title on
// the same calendar on touching days) are merged into one row that covers the whole span,
// so a "Head Office" entry per day reads as one line instead of one per day.
type calendarAgendaRow struct {
	Event     CalendarEvent // first event of the run; the row opens its details
	StartDate string        // all-day rows: first day; EndDate is exclusive, as in iCalendar
	EndDate   string
	TimeLabel string
	SpanLabel string // "Oct 19–21" for an all-day row over several days, else ""
}

type calendarAgendaDay struct {
	Key   string // 2006-01-02, the day the rows start on
	Label string
	Rows  []calendarAgendaRow
}

type calendarAgenda struct {
	Days        []calendarAgendaDay
	MultiSource bool // events from more than one calendar are in view, so rows name theirs
}

// calendarAgendaGroups buckets the view's events under their start day. Hidden calendars'
// events stay in the output (the page script reveals them when a calendar is turned on)
// but do not count toward MultiSource.
func calendarAgendaGroups(month CalendarMonthData) calendarAgenda {
	location := month.Month.Location()
	firstDay := ""
	if len(month.Weeks) > 0 && len(month.Weeks[0].Days) > 0 {
		firstDay = month.Weeks[0].Days[0].ISODate
	}
	sources := map[string]bool{}
	var allDay, timed []CalendarEvent
	for _, event := range month.Events {
		if !event.SourceHidden {
			sources[event.SourceID] = true
		}
		if event.AllDay {
			allDay = append(allDay, event)
		} else {
			timed = append(timed, event)
		}
	}

	var rows []calendarAgendaRow
	sort.SliceStable(allDay, func(i, j int) bool { return allDay[i].StartDate < allDay[j].StartDate })
	open := map[string]int{} // source+title -> index in rows of the run still being extended
	for _, event := range allDay {
		end := event.EndDate
		if end <= event.StartDate {
			end = calendarAddDays(event.StartDate, 1, location)
		}
		key := event.SourceID + "\x00" + calendarEventSummary(event)
		if index, ok := open[key]; ok && event.StartDate <= rows[index].EndDate {
			if end > rows[index].EndDate {
				rows[index].EndDate = end
			}
			continue
		}
		open[key] = len(rows)
		rows = append(rows, calendarAgendaRow{Event: event, StartDate: event.StartDate, EndDate: end, TimeLabel: "All day"})
	}
	for index := range rows {
		rows[index].SpanLabel = calendarAgendaSpanLabel(rows[index].StartDate, rows[index].EndDate, location)
	}
	for _, event := range timed {
		rows = append(rows, calendarAgendaRow{Event: event, StartDate: event.StartDate, EndDate: event.EndDate, TimeLabel: calendarEventAgendaTimeLabel(event, location)})
	}

	byDay := map[string]*calendarAgendaDay{}
	var days []*calendarAgendaDay
	for _, row := range rows {
		key := row.StartDate
		if !row.Event.AllDay && row.Event.StartAt != nil {
			key = row.Event.StartAt.In(location).Format("2006-01-02")
		}
		if firstDay != "" && key < firstDay {
			key = firstDay // started before the view; list it on the view's first day
		}
		day := byDay[key]
		if day == nil {
			day = &calendarAgendaDay{Key: key, Label: calendarAgendaDayLabel(key, month.TodayDateKey, location)}
			byDay[key] = day
			days = append(days, day)
		}
		day.Rows = append(day.Rows, row)
	}
	sort.SliceStable(days, func(i, j int) bool { return days[i].Key < days[j].Key })
	result := calendarAgenda{MultiSource: len(sources) > 1}
	for _, day := range days {
		// All-day rows lead the day, then timed rows by start.
		sort.SliceStable(day.Rows, func(i, j int) bool {
			a, b := day.Rows[i].Event, day.Rows[j].Event
			if a.AllDay != b.AllDay {
				return a.AllDay
			}
			if a.AllDay || a.StartAt == nil || b.StartAt == nil {
				return false
			}
			return a.StartAt.Before(*b.StartAt)
		})
		result.Days = append(result.Days, *day)
	}
	return result
}

func calendarAddDays(date string, days int, location *time.Location) string {
	parsed, err := time.ParseInLocation("2006-01-02", date, location)
	if err != nil {
		return date
	}
	return parsed.AddDate(0, 0, days).Format("2006-01-02")
}

func calendarAgendaDayLabel(key, today string, location *time.Location) string {
	if key == today {
		return "Today"
	}
	if today != "" && key == calendarAddDays(today, 1, location) {
		return "Tomorrow"
	}
	parsed, err := time.ParseInLocation("2006-01-02", key, location)
	if err != nil {
		return key
	}
	if todayDate, err := time.ParseInLocation("2006-01-02", today, location); err == nil && todayDate.Year() != parsed.Year() {
		return parsed.Format("Mon, Jan 2, 2006")
	}
	return parsed.Format("Mon, Jan 2")
}

// calendarAgendaSpanLabel names an all-day run's days, e.g. "Oct 19–21" or "Oct 30–Nov 2".
// end is exclusive. A one-day run has no label.
func calendarAgendaSpanLabel(start, end string, location *time.Location) string {
	first, err := time.ParseInLocation("2006-01-02", start, location)
	if err != nil {
		return ""
	}
	endDay, err := time.ParseInLocation("2006-01-02", end, location)
	if err != nil {
		return ""
	}
	last := endDay.AddDate(0, 0, -1)
	switch {
	case !last.After(first):
		return ""
	case last.Month() == first.Month() && last.Year() == first.Year():
		return first.Format("Jan 2") + "–" + last.Format("2")
	}
	return first.Format("Jan 2") + "–" + last.Format("Jan 2")
}

func calendarAgendaRowLabel(row calendarAgendaRow) string {
	label := calendarEventResponseSummary(row.Event)
	if row.SpanLabel != "" {
		label += " · " + row.SpanLabel
	}
	return label
}

// calendarAgendaResponseVisible: a reply status only earns space when it needs attention or
// differs from the default; "Accepted" is the normal case and stays unlabelled.
func calendarAgendaResponseVisible(event CalendarEvent) bool {
	switch calendarEventResponseState(event) {
	case "pending", "tentative", "declined":
		return true
	}
	return false
}

// calendarAgendaMeta is the quiet second line: the place, plus the calendar's name when
// events from several calendars are listed.
func calendarAgendaMeta(event CalendarEvent, multiSource bool) string {
	if multiSource {
		return calendarEventMeta(event)
	}
	return strings.TrimSpace(event.Location)
}
