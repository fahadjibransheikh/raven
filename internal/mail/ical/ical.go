// Package ical is a minimal, stdlib-only reader for the iCalendar (RFC 5545 /
// iMIP RFC 6047) invitations that arrive as text/calendar mail parts. It
// extracts only what the invitation card shows. Input is untrusted: size and
// line counts are capped and every failure is an error, never a panic.
package ical

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxBytes caps how much of an .ics is parsed.
	MaxBytes = 256 << 10
	// MaxLines caps unfolded content lines.
	MaxLines     = 5000
	maxAttendees = 200
	maxFieldLen  = 4096
)

var ErrNoEvent = errors.New("ical: no VEVENT")

type Person struct {
	Name  string
	Email string
}

type Attendee struct {
	Person
	PartStat string // ACCEPTED, DECLINED, TENTATIVE, NEEDS-ACTION, ...
}

// Invite is the first VEVENT of a VCALENDAR plus its METHOD.
type Invite struct {
	Method      string // upper-case: REQUEST, CANCEL, REPLY, PUBLISH, "" when absent
	UID         string
	Summary     string
	Description string
	Location    string
	Status      string // upper-case: CONFIRMED, TENTATIVE, CANCELLED
	Sequence    int
	Recurring   bool
	AllDay      bool
	Start       time.Time // instant (UTC-comparable); for all-day, midnight in the fallback zone
	End         time.Time // exclusive for all-day
	StartDate   string    // YYYY-MM-DD, all-day only
	EndDate     string    // YYYY-MM-DD exclusive, all-day only
	// TZUnknown is set when a TZID could not be resolved and the time was
	// read in the fallback zone instead.
	TZUnknown  bool
	Organizer  Person
	Attendees  []Attendee
	MeetingURL string // http(s) Meet/Teams/Zoom link, if any
}

type prop struct {
	name   string
	params map[string]string
	value  string
}

// Parse reads the first VEVENT (preferring one without RECURRENCE-ID).
// fallback is the zone for floating times and unrecognised TZIDs.
func Parse(data []byte, fallback *time.Location) (*Invite, error) {
	if fallback == nil {
		fallback = time.UTC
	}
	if len(data) > MaxBytes {
		data = data[:MaxBytes]
	}
	lines := unfold(string(data))

	var method string
	var events [][]prop
	var cur []prop
	inEvent, skipDepth := false, 0
	for _, ln := range lines {
		p, ok := parseLine(ln)
		if !ok {
			continue
		}
		switch p.name {
		case "BEGIN":
			v := strings.ToUpper(p.value)
			switch {
			case inEvent && skipDepth == 0 && v == "VALARM":
				skipDepth = 1
			case inEvent && skipDepth > 0:
				skipDepth++
			case !inEvent && v == "VEVENT":
				inEvent, cur = true, nil
			}
			continue
		case "END":
			v := strings.ToUpper(p.value)
			if skipDepth > 0 {
				skipDepth--
			} else if inEvent && v == "VEVENT" {
				events = append(events, cur)
				inEvent = false
			}
			continue
		}
		if inEvent && skipDepth == 0 {
			cur = append(cur, p)
		} else if !inEvent && p.name == "METHOD" {
			method = strings.ToUpper(strings.TrimSpace(p.value))
		}
	}
	if len(events) == 0 {
		return nil, ErrNoEvent
	}
	ev := events[0]
	for _, e := range events {
		if !hasProp(e, "RECURRENCE-ID") {
			ev = e
			break
		}
	}

	inv := &Invite{Method: method}
	var dtStart, dtEnd *prop
	var extra []string
	for i := range ev {
		p := &ev[i]
		switch p.name {
		case "UID":
			inv.UID = clip(strings.TrimSpace(p.value))
		case "SUMMARY":
			inv.Summary = clip(unescapeText(p.value))
		case "DESCRIPTION":
			inv.Description = clip(unescapeText(p.value))
		case "LOCATION":
			inv.Location = clip(unescapeText(p.value))
		case "STATUS":
			inv.Status = strings.ToUpper(strings.TrimSpace(p.value))
		case "SEQUENCE":
			inv.Sequence, _ = strconv.Atoi(strings.TrimSpace(p.value))
		case "RRULE", "RDATE":
			inv.Recurring = true
		case "DTSTART":
			dtStart = p
		case "DTEND":
			dtEnd = p
		case "ORGANIZER":
			inv.Organizer = person(p)
		case "ATTENDEE":
			if len(inv.Attendees) < maxAttendees {
				inv.Attendees = append(inv.Attendees, Attendee{Person: person(p), PartStat: strings.ToUpper(p.params["PARTSTAT"])})
			}
		case "X-GOOGLE-CONFERENCE", "X-MICROSOFT-SKYPETEAMSMEETINGURL", "X-MICROSOFT-ONLINEMEETINGEXTERNALLINK":
			extra = append(extra, p.value)
		}
	}
	if dtStart != nil {
		inv.Start, inv.AllDay, inv.TZUnknown = parseTime(dtStart, fallback)
		if inv.AllDay {
			inv.StartDate = inv.Start.Format("2006-01-02")
		}
		if dtEnd != nil {
			var unknown bool
			inv.End, _, unknown = parseTime(dtEnd, fallback)
			inv.TZUnknown = inv.TZUnknown || unknown
		} else if inv.AllDay {
			inv.End = inv.Start.AddDate(0, 0, 1)
		} else {
			inv.End = inv.Start
		}
		if inv.AllDay {
			inv.EndDate = inv.End.Format("2006-01-02")
		}
	}
	inv.MeetingURL = findMeetingURL(append(extra, inv.Location, inv.Description))
	return inv, nil
}

func hasProp(ps []prop, name string) bool {
	for _, p := range ps {
		if p.name == name {
			return true
		}
	}
	return false
}

func clip(s string) string {
	if len(s) > maxFieldLen {
		s = s[:maxFieldLen]
	}
	return strings.ToValidUTF8(s, "") // also drops a rune cut by the cap
}

// unfold joins continuation lines (a leading space or tab) and caps the count.
func unfold(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var out []string
	for _, raw := range strings.Split(s, "\n") {
		if (strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t")) && len(out) > 0 {
			if len(out[len(out)-1]) < 4*maxFieldLen {
				out[len(out)-1] += raw[1:]
			}
			continue
		}
		if len(out) >= MaxLines {
			break
		}
		out = append(out, raw)
	}
	return out
}

// parseLine splits "NAME;P=V;P2="a:b":value", honouring quoted parameter values.
func parseLine(ln string) (prop, bool) {
	inQ, colon := false, -1
	for i := 0; i < len(ln); i++ {
		switch ln[i] {
		case '"':
			inQ = !inQ
		case ':':
			if !inQ {
				colon = i
			}
		}
		if colon >= 0 {
			break
		}
	}
	if colon <= 0 {
		return prop{}, false
	}
	head, value := ln[:colon], ln[colon+1:]
	parts := splitOutsideQuotes(head, ';')
	p := prop{name: strings.ToUpper(strings.TrimSpace(parts[0])), value: value}
	for _, kv := range parts[1:] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if p.params == nil {
			p.params = map[string]string{}
		}
		p.params[strings.ToUpper(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	return p, p.name != ""
}

func splitOutsideQuotes(s string, sep byte) []string {
	var out []string
	inQ, start := false, 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"':
			inQ = !inQ
		case s[i] == sep && !inQ:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// unescapeText reverses RFC 5545 TEXT escaping.
func unescapeText(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n', 'N':
				b.WriteByte('\n')
			default: // \\ \, \; and anything else: the literal character
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func person(p *prop) Person {
	email := strings.TrimSpace(p.value)
	if len(email) >= 7 && strings.EqualFold(email[:7], "mailto:") {
		email = email[7:]
	}
	return Person{Name: clip(p.params["CN"]), Email: clip(strings.TrimSpace(email))}
}

// windowsZones maps the Windows zone names Exchange/Outlook put in TZID to IANA.
var windowsZones = map[string]string{
	"Pacific Standard Time": "America/Los_Angeles", "Mountain Standard Time": "America/Denver",
	"US Mountain Standard Time": "America/Phoenix", "Central Standard Time": "America/Chicago",
	"Eastern Standard Time": "America/New_York", "Atlantic Standard Time": "America/Halifax",
	"Alaskan Standard Time": "America/Anchorage", "Hawaiian Standard Time": "Pacific/Honolulu",
	"Canada Central Standard Time": "America/Regina", "Newfoundland Standard Time": "America/St_Johns",
	"Central America Standard Time": "America/Guatemala", "Mexico Standard Time": "America/Mexico_City",
	"E. South America Standard Time": "America/Sao_Paulo", "Argentina Standard Time": "America/Argentina/Buenos_Aires",
	"UTC": "UTC", "GMT Standard Time": "Europe/London", "Greenwich Standard Time": "Atlantic/Reykjavik",
	"W. Europe Standard Time": "Europe/Berlin", "Central Europe Standard Time": "Europe/Budapest",
	"Central European Standard Time": "Europe/Warsaw", "Romance Standard Time": "Europe/Paris",
	"GTB Standard Time": "Europe/Bucharest", "FLE Standard Time": "Europe/Kiev", "E. Europe Standard Time": "Europe/Chisinau",
	"Russian Standard Time": "Europe/Moscow", "Turkey Standard Time": "Europe/Istanbul",
	"Israel Standard Time": "Asia/Jerusalem", "Egypt Standard Time": "Africa/Cairo",
	"South Africa Standard Time": "Africa/Johannesburg", "W. Central Africa Standard Time": "Africa/Lagos",
	"E. Africa Standard Time": "Africa/Nairobi", "Arab Standard Time": "Asia/Riyadh",
	"Arabian Standard Time": "Asia/Dubai", "Pakistan Standard Time": "Asia/Karachi",
	"India Standard Time": "Asia/Kolkata", "Bangladesh Standard Time": "Asia/Dhaka",
	"SE Asia Standard Time": "Asia/Bangkok", "China Standard Time": "Asia/Shanghai",
	"Singapore Standard Time": "Asia/Singapore", "Taipei Standard Time": "Asia/Taipei",
	"Tokyo Standard Time": "Asia/Tokyo", "Korea Standard Time": "Asia/Seoul",
	"AUS Eastern Standard Time": "Australia/Sydney", "E. Australia Standard Time": "Australia/Brisbane",
	"Cen. Australia Standard Time": "Australia/Adelaide", "W. Australia Standard Time": "Australia/Perth",
	"New Zealand Standard Time": "Pacific/Auckland",
}

// resolveZone returns the location for a TZID. Callers treat nil as unknown.
func resolveZone(tzid string) *time.Location {
	tzid = strings.TrimSpace(strings.TrimPrefix(tzid, "/")) // some producers emit "/mozilla.org/.../Europe/Paris"
	if tzid == "" {
		return nil
	}
	if iana, ok := windowsZones[tzid]; ok {
		tzid = iana
	}
	if loc, err := time.LoadLocation(tzid); err == nil {
		return loc
	}
	return nil
}

// parseTime handles DATE, floating, UTC ("Z") and TZID date-times. The second
// result reports an all-day value, the third an unresolved TZID.
func parseTime(p *prop, fallback *time.Location) (t time.Time, allDay, tzUnknown bool) {
	v := strings.TrimSpace(p.value)
	if strings.EqualFold(p.params["VALUE"], "DATE") || len(v) == 8 {
		d, err := time.ParseInLocation("20060102", v, fallback)
		if err != nil {
			return time.Time{}, false, false
		}
		return d, true, false
	}
	if strings.HasSuffix(v, "Z") || strings.HasSuffix(v, "z") {
		d, err := time.Parse("20060102T150405Z", strings.ToUpper(v))
		if err != nil {
			return time.Time{}, false, false
		}
		return d, false, false
	}
	loc := fallback
	if tzid := p.params["TZID"]; tzid != "" {
		if l := resolveZone(tzid); l != nil {
			loc = l
		} else {
			tzUnknown = true
		}
	}
	d, err := time.ParseInLocation("20060102T150405", v, loc)
	if err != nil {
		return time.Time{}, false, false
	}
	return d, false, tzUnknown
}

var urlRe = regexp.MustCompile(`https?://[^\s<>"'\\]+`)

// findMeetingURL returns the first Google Meet, Teams or Zoom link in the
// texts. Only http(s) URLs on those hosts qualify.
func findMeetingURL(texts []string) string {
	for _, t := range texts {
		for _, m := range urlRe.FindAllString(unescapeText(t), -1) {
			m = strings.TrimRight(m, ".,;)>]")
			u, err := url.Parse(m)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				continue
			}
			h := strings.ToLower(u.Hostname())
			if h == "meet.google.com" || h == "teams.microsoft.com" || h == "teams.live.com" || h == "zoom.us" || strings.HasSuffix(h, ".zoom.us") {
				return m
			}
		}
	}
	return ""
}

// IsHTTPURL reports whether s is a plain http(s) URL, for linkifying fields.
func IsHTTPURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
