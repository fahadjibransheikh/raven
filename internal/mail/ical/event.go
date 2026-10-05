package ical

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Event is one VEVENT of a calendar object, read for sync (all of them, not
// just the first as Parse does).
type Event struct {
	Comp         *Comp
	UID          string
	Summary      string
	Description  string
	Location     string
	Status       string // upper-case
	Sequence     int
	AllDay       bool
	Start, End   time.Time // instants; all-day: dates at 00:00 UTC (End exclusive)
	Loc          *time.Location
	TZID         string // IANA name when DTSTART carried a resolvable TZID, else ""
	RRule        string
	HasRDate     bool
	ExDates      []time.Time // instants; all-day: 00:00 UTC of the date
	RecurrenceID *time.Time  // same convention as Start
	Organizer    Person
	Attendees    []Attendee
	MeetingURL   string
	LastModified time.Time
}

// Resource is a parsed calendar object resource (.ics): the VCALENDAR plus its events.
type Resource struct {
	Root   *Comp
	Events []*Event
}

// ParseResource reads every VEVENT. fallback is the zone for floating times.
func ParseResource(data []byte, fallback *time.Location) (*Resource, error) {
	if fallback == nil {
		fallback = time.UTC
	}
	root, err := ParseDoc(data)
	if err != nil {
		return nil, err
	}
	r := &Resource{Root: root}
	for _, c := range root.Children {
		if c.Name == "VEVENT" {
			r.Events = append(r.Events, eventFromComp(c, fallback))
		}
	}
	if len(r.Events) == 0 {
		return nil, ErrNoEvent
	}
	return r, nil
}

// Master is the VEVENT without RECURRENCE-ID (nil for an overrides-only object).
func (r *Resource) Master() *Event {
	for _, e := range r.Events {
		if e.RecurrenceID == nil {
			return e
		}
	}
	return nil
}

// Override returns the VEVENT whose RECURRENCE-ID is the given instant.
func (r *Resource) Override(recID time.Time) *Event {
	for _, e := range r.Events {
		if e.RecurrenceID != nil && e.RecurrenceID.Equal(recID) {
			return e
		}
	}
	return nil
}

func propToParsed(p Prop) *prop {
	_, params, value := p.Head, map[string]string{}, p.Value
	parts := splitOutsideQuotes(p.Head, ';')
	for _, kv := range parts[1:] {
		if k, v, ok := strings.Cut(kv, "="); ok {
			params[strings.ToUpper(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return &prop{name: p.Name(), params: params, value: value}
}

func dayUTC(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// parseEventTime is parseTime with the all-day convention of this file.
func parseEventTime(p Prop, fallback *time.Location) (t time.Time, allDay bool, loc *time.Location, tzid string, ok bool) {
	pp := propToParsed(p)
	v := strings.TrimSpace(pp.value)
	pt, ad, unknown := parseTime(pp, fallback)
	if pt.IsZero() {
		return time.Time{}, false, nil, "", false
	}
	if ad {
		return dayUTC(pt), true, time.UTC, "", true
	}
	if strings.HasSuffix(v, "Z") || strings.HasSuffix(v, "z") {
		return pt, false, time.UTC, "", true
	}
	if z := pp.params["TZID"]; z != "" && !unknown {
		if l := resolveZone(z); l != nil {
			return pt, false, l, l.String(), true
		}
	}
	return pt, false, fallback, "", true
}

func eventFromComp(c *Comp, fallback *time.Location) *Event {
	e := &Event{Comp: c, Loc: time.UTC}
	var extra []string
	for _, p := range c.Props {
		switch p.Name() {
		case "UID":
			e.UID = clip(strings.TrimSpace(p.Value))
		case "SUMMARY":
			e.Summary = clip(unescapeText(p.Value))
		case "DESCRIPTION":
			e.Description = clip(unescapeText(p.Value))
		case "LOCATION":
			e.Location = clip(unescapeText(p.Value))
		case "STATUS":
			e.Status = strings.ToUpper(strings.TrimSpace(p.Value))
		case "SEQUENCE":
			e.Sequence, _ = strconv.Atoi(strings.TrimSpace(p.Value))
		case "RRULE":
			e.RRule = strings.TrimSpace(p.Value)
		case "RDATE":
			e.HasRDate = true
		case "ORGANIZER":
			e.Organizer = person(propToParsed(p))
		case "ATTENDEE":
			if len(e.Attendees) < maxAttendees {
				pp := propToParsed(p)
				e.Attendees = append(e.Attendees, Attendee{Person: person(pp), PartStat: strings.ToUpper(pp.params["PARTSTAT"])})
			}
		case "X-GOOGLE-CONFERENCE", "X-MICROSOFT-SKYPETEAMSMEETINGURL", "X-MICROSOFT-ONLINEMEETINGEXTERNALLINK", "URL":
			extra = append(extra, p.Value)
		case "LAST-MODIFIED":
			if t, _, _, _, ok := parseEventTime(p, time.UTC); ok {
				e.LastModified = t
			}
		case "RECURRENCE-ID":
			if t, _, _, _, ok := parseEventTime(p, fallback); ok {
				e.RecurrenceID = &t
			}
		case "EXDATE":
			for _, v := range strings.Split(p.Value, ",") {
				if t, _, _, _, ok := parseEventTime(Prop{Head: p.Head, Value: v}, fallback); ok {
					e.ExDates = append(e.ExDates, t)
				}
			}
		}
	}
	if p := c.Get("DTSTART"); p != nil {
		if t, ad, loc, tzid, ok := parseEventTime(*p, fallback); ok {
			e.Start, e.AllDay, e.Loc, e.TZID = t, ad, loc, tzid
		}
	}
	switch {
	case c.Get("DTEND") != nil:
		if t, _, _, _, ok := parseEventTime(*c.Get("DTEND"), fallback); ok {
			e.End = t
		}
	case c.Get("DURATION") != nil:
		if d, ok := parseDuration(c.Get("DURATION").Value); ok {
			e.End = e.Start.Add(d)
		}
	}
	if e.End.IsZero() || e.End.Before(e.Start) {
		if e.AllDay {
			e.End = e.Start.AddDate(0, 0, 1)
		} else {
			e.End = e.Start
		}
	}
	e.MeetingURL = findMeetingURL(append(extra, e.Location, e.Description))
	return e
}

// parseDuration reads the RFC 5545 DURATION subset P[n]W / P[n]D[T[n]H[n]M[n]S].
func parseDuration(s string) (time.Duration, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	neg := false
	if strings.HasPrefix(s, "-") {
		neg, s = true, s[1:]
	} else {
		s = strings.TrimPrefix(s, "+")
	}
	if !strings.HasPrefix(s, "P") {
		return 0, false
	}
	s = s[1:]
	var d time.Duration
	inTime, num := false, ""
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			num += string(r)
		case r == 'T':
			inTime = true
		default:
			n, err := strconv.Atoi(num)
			if err != nil {
				return 0, false
			}
			num = ""
			switch {
			case r == 'W' && !inTime:
				d += time.Duration(n) * 7 * 24 * time.Hour
			case r == 'D' && !inTime:
				d += time.Duration(n) * 24 * time.Hour
			case r == 'H' && inTime:
				d += time.Duration(n) * time.Hour
			case r == 'M' && inTime:
				d += time.Duration(n) * time.Minute
			case r == 'S' && inTime:
				d += time.Duration(n) * time.Second
			default:
				return 0, false
			}
		}
	}
	if num != "" {
		return 0, false
	}
	if neg {
		d = -d
	}
	return d, true
}

// ---------- RRULE ----------

// ErrUnsupportedRRule means the rule uses parts outside the supported subset.
var ErrUnsupportedRRule = errors.New("ical: unsupported RRULE")

type weekdaySpec struct {
	ord int // 0 = every, else nth (negative from the end)
	day time.Weekday
}

type rrule struct {
	freq       string
	interval   int
	count      int // 0 = none
	until      time.Time
	hasUntil   bool
	untilDate  bool
	byDay      []weekdaySpec
	byMonthDay []int
	byMonth    []int
	wkst       time.Weekday
}

var weekdayCodes = map[string]time.Weekday{"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday, "TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday}

func parseIntList(s string, lo, hi int) ([]int, bool) {
	var out []int
	for _, f := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n == 0 || n < lo || n > hi {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

func parseRRule(s string, loc *time.Location) (rrule, error) {
	r := rrule{interval: 1, wkst: time.Monday}
	bad := func(format string, a ...any) (rrule, error) {
		return r, fmt.Errorf("%w: "+format, append([]any{ErrUnsupportedRRule}, a...)...)
	}
	for _, part := range strings.Split(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(s)), "RRULE:"), ";") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return bad("malformed part %q", part)
		}
		switch k {
		case "FREQ":
			if v != "DAILY" && v != "WEEKLY" && v != "MONTHLY" && v != "YEARLY" {
				return bad("FREQ=%s", v)
			}
			r.freq = v
		case "INTERVAL":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 1000 {
				return bad("INTERVAL=%s", v)
			}
			r.interval = n
		case "COUNT":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return bad("COUNT=%s", v)
			}
			r.count = n
		case "UNTIL":
			r.hasUntil = true
			switch {
			case len(v) == 8:
				d, err := time.Parse("20060102", v)
				if err != nil {
					return bad("UNTIL=%s", v)
				}
				r.until, r.untilDate = d, true
			case strings.HasSuffix(v, "Z"):
				d, err := time.Parse("20060102T150405Z", v)
				if err != nil {
					return bad("UNTIL=%s", v)
				}
				r.until = d
			default:
				d, err := time.ParseInLocation("20060102T150405", v, loc)
				if err != nil {
					return bad("UNTIL=%s", v)
				}
				r.until = d
			}
		case "BYDAY":
			for _, f := range strings.Split(v, ",") {
				if len(f) < 2 {
					return bad("BYDAY=%s", v)
				}
				d, ok := weekdayCodes[f[len(f)-2:]]
				if !ok {
					return bad("BYDAY=%s", v)
				}
				ord := 0
				if o := f[:len(f)-2]; o != "" {
					n, err := strconv.Atoi(o)
					if err != nil || n == 0 || n < -53 || n > 53 {
						return bad("BYDAY=%s", v)
					}
					ord = n
				}
				r.byDay = append(r.byDay, weekdaySpec{ord, d})
			}
		case "BYMONTHDAY":
			l, ok := parseIntList(v, -31, 31)
			if !ok {
				return bad("BYMONTHDAY=%s", v)
			}
			r.byMonthDay = l
		case "BYMONTH":
			l, ok := parseIntList(v, 1, 12)
			if !ok {
				return bad("BYMONTH=%s", v)
			}
			r.byMonth = l
		case "WKST":
			d, ok := weekdayCodes[v]
			if !ok {
				return bad("WKST=%s", v)
			}
			r.wkst = d
		default: // BYSETPOS, BYYEARDAY, BYWEEKNO, BYHOUR, ...
			return bad("%s", k)
		}
	}
	if r.freq == "" {
		return bad("missing FREQ")
	}
	if r.count > 0 && r.hasUntil {
		return bad("COUNT and UNTIL together")
	}
	ordinals := false
	for _, d := range r.byDay {
		ordinals = ordinals || d.ord != 0
	}
	switch r.freq {
	case "DAILY", "WEEKLY":
		if ordinals {
			return bad("ordinal BYDAY with %s", r.freq)
		}
		if r.freq == "WEEKLY" && len(r.byMonthDay) > 0 {
			return bad("BYMONTHDAY with WEEKLY")
		}
	case "YEARLY":
		if len(r.byMonth) == 0 && (len(r.byMonthDay) > 0 || len(r.byDay) > 0) {
			return bad("YEARLY BYDAY/BYMONTHDAY without BYMONTH")
		}
	}
	return r, nil
}

func daysIn(y int, m time.Month) int { return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day() }

func inInts(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (r rrule) monthDays(y int, m time.Month, startDay int) []time.Time {
	n := daysIn(y, m)
	var days []int
	switch {
	case len(r.byMonthDay) > 0:
		for _, d := range r.byMonthDay {
			if d < 0 {
				d = n + 1 + d
			}
			if d >= 1 && d <= n {
				days = append(days, d)
			}
		}
		if len(r.byDay) > 0 { // BYDAY narrows BYMONTHDAY
			kept := days[:0]
			for _, d := range days {
				wd := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Weekday()
				for _, s := range r.byDay {
					if s.day == wd {
						kept = append(kept, d)
						break
					}
				}
			}
			days = kept
		}
	case len(r.byDay) > 0:
		for _, s := range r.byDay {
			var match []int
			for d := 1; d <= n; d++ {
				if time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Weekday() == s.day {
					match = append(match, d)
				}
			}
			switch {
			case s.ord == 0:
				days = append(days, match...)
			case s.ord > 0 && s.ord <= len(match):
				days = append(days, match[s.ord-1])
			case s.ord < 0 && -s.ord <= len(match):
				days = append(days, match[len(match)+s.ord])
			}
		}
	default:
		if startDay <= n {
			days = append(days, startDay)
		}
	}
	sort.Ints(days)
	out := make([]time.Time, 0, len(days))
	for i, d := range days {
		if i == 0 || d != days[i-1] {
			out = append(out, time.Date(y, m, d, 0, 0, 0, 0, time.UTC))
		}
	}
	return out
}

// dates returns the candidate dates (UTC midnights) of period k, ascending.
func (r rrule) dates(k int, start time.Time) []time.Time {
	byMonthOK := func(m time.Month) bool { return len(r.byMonth) == 0 || inInts(r.byMonth, int(m)) }
	switch r.freq {
	case "DAILY":
		d := start.AddDate(0, 0, k*r.interval)
		if !byMonthOK(d.Month()) || (len(r.byMonthDay) > 0 && !inInts(r.byMonthDay, d.Day()) && !inInts(r.byMonthDay, d.Day()-daysIn(d.Year(), d.Month())-1)) {
			return nil
		}
		if len(r.byDay) > 0 {
			ok := false
			for _, s := range r.byDay {
				ok = ok || s.day == d.Weekday()
			}
			if !ok {
				return nil
			}
		}
		return []time.Time{d}
	case "WEEKLY":
		ws := start.AddDate(0, 0, -int((start.Weekday()-r.wkst+7)%7))
		wk := ws.AddDate(0, 0, 7*k*r.interval)
		var out []time.Time
		days := []time.Weekday{start.Weekday()}
		if len(r.byDay) > 0 {
			days = days[:0]
			for _, s := range r.byDay {
				days = append(days, s.day)
			}
		}
		for _, wd := range days {
			d := wk.AddDate(0, 0, int((wd-r.wkst+7)%7))
			if byMonthOK(d.Month()) {
				out = append(out, d)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
		return out
	case "MONTHLY":
		idx := int(start.Month()) - 1 + k*r.interval
		y, m := start.Year()+idx/12, time.Month(idx%12+1)
		if !byMonthOK(m) {
			return nil
		}
		return r.monthDays(y, m, start.Day())
	default: // YEARLY
		y := start.Year() + k*r.interval
		months := r.byMonth
		if len(months) == 0 {
			months = []int{int(start.Month())}
		}
		sorted := append([]int(nil), months...)
		sort.Ints(sorted)
		var out []time.Time
		for _, m := range sorted {
			out = append(out, r.monthDays(y, time.Month(m), start.Day())...)
		}
		return out
	}
}

// ---------- expansion ----------

// Instance is one occurrence of an event within a window.
type Instance struct {
	Event        *Event // the master, or the override VEVENT for this occurrence
	Start, End   time.Time
	RecurrenceID time.Time // identity of the occurrence (original start); zero for a plain event
	Override     bool
}

const (
	maxExpandPeriods   = 20000
	maxExpandInstances = 5000
)

func overlaps(start, end, from, to time.Time) bool {
	return start.Before(to) && (end.After(from) || (end.Equal(start) && !start.Before(from)))
}

// Instances expands the object into the occurrences overlapping [from, to).
// When the master's rule is outside the supported subset the result holds only
// the first occurrence (plus any overrides) and err wraps ErrUnsupportedRRule;
// the instances are still usable.
func (r *Resource) Instances(from, to time.Time) ([]Instance, error) {
	master := r.Master()
	var out []Instance
	var rerr error
	overrides := map[int64]*Event{}
	for _, e := range r.Events {
		if e.RecurrenceID != nil {
			overrides[e.RecurrenceID.UnixNano()] = e
		}
	}
	emit := func(e *Event, start, end, recID time.Time, override bool) {
		if e.Status == "CANCELLED" || !overlaps(start, end, from, to) || len(out) >= maxExpandInstances {
			return
		}
		out = append(out, Instance{Event: e, Start: start, End: end, RecurrenceID: recID, Override: override})
	}
	used := map[int64]bool{}
	if master != nil && !master.Start.IsZero() {
		if master.RRule == "" && !master.HasRDate && len(overrides) == 0 {
			emit(master, master.Start, master.End, time.Time{}, false)
			return out, nil
		}
		occ, err := r.occurrences(master, to)
		if err != nil {
			rerr = err
			occ = []time.Time{master.Start} // first occurrence only
		}
		for _, s := range occ {
			if ov := overrides[s.UnixNano()]; ov != nil {
				used[s.UnixNano()] = true
				emit(ov, ov.Start, ov.End, s, true)
				continue
			}
			excluded := false
			for _, x := range master.ExDates {
				excluded = excluded || x.Equal(s)
			}
			if !excluded {
				emit(master, s, s.Add(master.occurrenceLength(s)), s, false)
			}
		}
	}
	for key, ov := range overrides {
		if !used[key] {
			emit(ov, ov.Start, ov.End, *ov.RecurrenceID, true)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, rerr
}

// occurrenceLength is the master's span; timed events keep the absolute length
// across DST, all-day events keep their day count.
func (e *Event) occurrenceLength(start time.Time) time.Duration {
	if e.AllDay {
		days := int(e.End.Sub(e.Start).Hours() / 24)
		return start.AddDate(0, 0, days).Sub(start)
	}
	return e.End.Sub(e.Start)
}

// occurrences lists start instants of the master's series up to `to`
// (exclusive), including those removed by EXDATE; the caller filters.
func (r *Resource) occurrences(m *Event, to time.Time) ([]time.Time, error) {
	if m.HasRDate && m.RRule == "" {
		return nil, fmt.Errorf("%w: RDATE without RRULE", ErrUnsupportedRRule)
	}
	if m.RRule == "" {
		return []time.Time{m.Start}, nil
	}
	rule, err := parseRRule(m.RRule, m.Loc)
	if err != nil {
		return nil, err
	}
	if m.HasRDate {
		return nil, fmt.Errorf("%w: RDATE", ErrUnsupportedRRule)
	}
	loc := m.Loc
	wall := m.Start.In(loc)
	startDate := time.Date(wall.Year(), wall.Month(), wall.Day(), 0, 0, 0, 0, time.UTC)
	at := func(d time.Time) time.Time {
		if m.AllDay {
			return d
		}
		return time.Date(d.Year(), d.Month(), d.Day(), wall.Hour(), wall.Minute(), wall.Second(), 0, loc)
	}
	pastUntil := func(s time.Time) bool {
		if !rule.hasUntil {
			return false
		}
		if rule.untilDate && !m.AllDay {
			ud := rule.until
			return !s.Before(time.Date(ud.Year(), ud.Month(), ud.Day()+1, 0, 0, 0, 0, loc))
		}
		return s.After(rule.until)
	}
	out := []time.Time{m.Start}
	n := 1
	if rule.count == 1 {
		return out, nil
	}
	for k := 0; k < maxExpandPeriods && len(out) < maxExpandInstances; k++ {
		stop := false
		for _, d := range rule.dates(k, startDate) {
			s := at(d)
			if !s.After(m.Start) {
				continue
			}
			if pastUntil(s) || !s.Before(to) {
				stop = true
				break
			}
			out = append(out, s)
			n++
			if rule.count > 0 && n >= rule.count {
				return out, nil
			}
		}
		if stop {
			break
		}
	}
	return out, nil
}
