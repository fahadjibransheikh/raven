package ical

import (
	"strings"
	"testing"
	"time"
)

func crlf(s string) []byte {
	return []byte(strings.ReplaceAll(strings.TrimLeft(s, "\n"), "\n", "\r\n"))
}

var la = mustLoc("America/Los_Angeles")

func mustLoc(n string) *time.Location {
	l, err := time.LoadLocation(n)
	if err != nil {
		panic(err)
	}
	return l
}

const googleInvite = `
BEGIN:VCALENDAR
PRODID:-//Google Inc//Google Calendar 70.9054//EN
VERSION:2.0
CALSCALE:GREGORIAN
METHOD:REQUEST
BEGIN:VEVENT
DTSTART:20261007T160000Z
DTEND:20261007T170000Z
DTSTAMP:20261001T120000Z
ORGANIZER;CN=Boss Person:mailto:boss@example.com
UID:abc123@google.com
ATTENDEE;CUTYPE=INDIVIDUAL;ROLE=REQ-PARTICIPANT;PARTSTAT=ACCEPTED;CN=Boss Person;X-NUM-GUESTS=0:mailto:boss@example.com
ATTENDEE;CUTYPE=INDIVIDUAL;ROLE=REQ-PARTICIPANT;PARTSTAT=NEEDS-ACTION;RSVP=TRUE;CN=user@example.com;X-NUM-GUESTS=0:mailto:user@example.com
X-GOOGLE-CONFERENCE:https://meet.google.com/abc-defg-hij
RRULE:FREQ=WEEKLY;BYDAY=WE
SEQUENCE:2
STATUS:CONFIRMED
SUMMARY:Weekly sync\, planning
LOCATION:Room 4\; Floor 2
DESCRIPTION:Line one\nLine two with a very long text that gets folded by the
  producer and continues here
END:VEVENT
END:VCALENDAR
`

func TestParseGoogle(t *testing.T) {
	inv, err := Parse(crlf(googleInvite), la)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Method != "REQUEST" || inv.UID != "abc123@google.com" || inv.Sequence != 2 || !inv.Recurring || inv.Status != "CONFIRMED" {
		t.Fatalf("header fields: %+v", inv)
	}
	if inv.Summary != "Weekly sync, planning" || inv.Location != "Room 4; Floor 2" {
		t.Fatalf("escaping: %q / %q", inv.Summary, inv.Location)
	}
	if want := "Line one\nLine two with a very long text that gets folded by the producer and continues here"; !strings.HasPrefix(inv.Description, "Line one\nLine two") || !strings.Contains(inv.Description, "folded by the producer") {
		t.Fatalf("unfolding: %q (want like %q)", inv.Description, want)
	}
	if !inv.Start.Equal(time.Date(2026, 10, 7, 16, 0, 0, 0, time.UTC)) || !inv.End.Equal(inv.Start.Add(time.Hour)) || inv.AllDay {
		t.Fatalf("times: %v - %v", inv.Start, inv.End)
	}
	if inv.Organizer.Email != "boss@example.com" || inv.Organizer.Name != "Boss Person" {
		t.Fatalf("organizer: %+v", inv.Organizer)
	}
	if len(inv.Attendees) != 2 || inv.Attendees[1].PartStat != "NEEDS-ACTION" || inv.Attendees[1].Email != "user@example.com" {
		t.Fatalf("attendees: %+v", inv.Attendees)
	}
	if inv.MeetingURL != "https://meet.google.com/abc-defg-hij" {
		t.Fatalf("meeting url: %q", inv.MeetingURL)
	}
}

const outlookInvite = `
BEGIN:VCALENDAR
METHOD:REQUEST
PRODID:Microsoft Exchange Server 2010
VERSION:2.0
BEGIN:VTIMEZONE
TZID:Eastern Standard Time
BEGIN:STANDARD
DTSTART:16010101T020000
TZOFFSETFROM:-0400
TZOFFSETTO:-0500
END:STANDARD
END:VTIMEZONE
BEGIN:VEVENT
ORGANIZER;CN="Doe, Jane":MAILTO:jane@contoso.com
ATTENDEE;ROLE=REQ-PARTICIPANT;PARTSTAT=NEEDS-ACTION;RSVP=TRUE;CN=Me:MAILTO:me@contoso.com
DESCRIPTION;LANGUAGE=en-US:Join: <https://teams.microsoft.com/l/meetup-join/19%3ameeting_abc/0?context=x>\nThanks
SUMMARY;LANGUAGE=en-US:Budget review
DTSTART;TZID="Eastern Standard Time":20261007T100000
DTEND;TZID="Eastern Standard Time":20261007T110000
UID:040000008200E00074C5B7101A82E008000000
CLASS:PUBLIC
BEGIN:VALARM
TRIGGER:-PT15M
ACTION:DISPLAY
DESCRIPTION:Reminder
END:VALARM
END:VEVENT
END:VCALENDAR
`

func TestParseOutlookWindowsTZID(t *testing.T) {
	inv, err := Parse(crlf(outlookInvite), la)
	if err != nil {
		t.Fatal(err)
	}
	if inv.TZUnknown {
		t.Fatal("Eastern Standard Time should be mapped")
	}
	if want := time.Date(2026, 10, 7, 14, 0, 0, 0, time.UTC); !inv.Start.Equal(want) {
		t.Fatalf("start = %v, want %v (10:00 EDT)", inv.Start.UTC(), want)
	}
	if inv.Organizer.Email != "jane@contoso.com" || inv.Organizer.Name != "Doe, Jane" {
		t.Fatalf("organizer %+v", inv.Organizer)
	}
	if inv.Summary != "Budget review" || inv.Description == "Reminder" {
		t.Fatalf("summary/description %q / %q (VALARM must be skipped)", inv.Summary, inv.Description)
	}
	if !strings.HasPrefix(inv.MeetingURL, "https://teams.microsoft.com/l/meetup-join/") {
		t.Fatalf("meeting url %q", inv.MeetingURL)
	}
}

func TestUnknownTZIDFallsBackAndFlags(t *testing.T) {
	data := crlf("BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:x\nDTSTART;TZID=\"(UTC+01:00) Somewhere Odd\":20261007T100000\nEND:VEVENT\nEND:VCALENDAR\n")
	inv, err := Parse(data, la)
	if err != nil {
		t.Fatal(err)
	}
	if !inv.TZUnknown {
		t.Fatal("want TZUnknown")
	}
	if want := time.Date(2026, 10, 7, 17, 0, 0, 0, time.UTC); !inv.Start.Equal(want) {
		t.Fatalf("start = %v, want %v (10:00 in fallback zone)", inv.Start.UTC(), want)
	}
}

const appleInvite = `
BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Apple Inc.//macOS 15.0//EN
CALSCALE:GREGORIAN
METHOD:REQUEST
BEGIN:VEVENT
CREATED:20261001T120000Z
UID:7C4D-APPLE-UID
DTSTART;TZID=Europe/Paris:20261008T090000
DTEND;TZID=Europe/Paris:20261008T093000
SUMMARY:Coffee
LOCATION:Caf\\U00e9 de Flore\n172 Boulevard Saint-Germain
ORGANIZER;CN=Anna;EMAIL=anna@icloud.com:mailto:anna@icloud.com
ATTENDEE;CN=Me;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:me@example.com
SEQUENCE:0
TRANSP:OPAQUE
END:VEVENT
END:VCALENDAR
`

func TestParseApple(t *testing.T) {
	inv, err := Parse(crlf(appleInvite), la)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 8, 7, 0, 0, 0, time.UTC); !inv.Start.Equal(want) {
		t.Fatalf("start %v want %v", inv.Start.UTC(), want)
	}
	if inv.End.Sub(inv.Start) != 30*time.Minute || inv.Recurring || inv.TZUnknown {
		t.Fatalf("%+v", inv)
	}
	if !strings.Contains(inv.Location, "\n172 Boulevard") {
		t.Fatalf("location %q", inv.Location)
	}
}

func TestAllDayIsExclusiveDateInFallbackZone(t *testing.T) {
	data := crlf("BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:d\nDTSTART;VALUE=DATE:20261010\nDTEND;VALUE=DATE:20261012\nSUMMARY:Offsite\nEND:VEVENT\nEND:VCALENDAR\n")
	inv, err := Parse(data, la)
	if err != nil {
		t.Fatal(err)
	}
	if !inv.AllDay || inv.StartDate != "2026-10-10" || inv.EndDate != "2026-10-12" {
		t.Fatalf("%+v", inv)
	}
	one := crlf("BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:d\nDTSTART;VALUE=DATE:20261010\nEND:VEVENT\nEND:VCALENDAR\n")
	inv, _ = Parse(one, la)
	if inv.EndDate != "2026-10-11" {
		t.Fatalf("missing DTEND should default to one day, got %q", inv.EndDate)
	}
}

func TestCancelAndReply(t *testing.T) {
	cancel := crlf("BEGIN:VCALENDAR\nMETHOD:CANCEL\nBEGIN:VEVENT\nUID:c1\nSTATUS:CANCELLED\nSUMMARY:Gone\nDTSTART:20261007T160000Z\nEND:VEVENT\nEND:VCALENDAR\n")
	inv, err := Parse(cancel, la)
	if err != nil || inv.Method != "CANCEL" || inv.Status != "CANCELLED" {
		t.Fatalf("%+v %v", inv, err)
	}
	reply := crlf("BEGIN:VCALENDAR\nMETHOD:REPLY\nBEGIN:VEVENT\nUID:c1\nSUMMARY:Lunch\nDTSTART:20261007T160000Z\nATTENDEE;PARTSTAT=DECLINED;CN=Bob:mailto:bob@example.com\nEND:VEVENT\nEND:VCALENDAR\n")
	inv, err = Parse(reply, la)
	if err != nil || inv.Method != "REPLY" || len(inv.Attendees) != 1 || inv.Attendees[0].PartStat != "DECLINED" || inv.Attendees[0].Name != "Bob" {
		t.Fatalf("%+v %v", inv, err)
	}
}

func TestPrefersEventWithoutRecurrenceID(t *testing.T) {
	data := crlf("BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:r\nRECURRENCE-ID:20261014T160000Z\nSUMMARY:Moved\nEND:VEVENT\nBEGIN:VEVENT\nUID:r\nSUMMARY:Series\nRRULE:FREQ=WEEKLY\nEND:VEVENT\nEND:VCALENDAR\n")
	inv, _ := Parse(data, la)
	if inv.Summary != "Series" {
		t.Fatalf("got %q", inv.Summary)
	}
}

func TestMeetingURLOnlyKnownHostsAndHTTP(t *testing.T) {
	cases := map[string]string{
		"see https://zoom.us/j/123?pwd=a.":                         "https://zoom.us/j/123?pwd=a",
		"https://us02web.zoom.us/j/9":                              "https://us02web.zoom.us/j/9",
		"javascript:alert(1) https://evil.example/meet.google.com": "",
		"https://notzoom.us/j/1":                                   "",
		"https://zoom.us.evil.example/j/1":                         "",
		"ftp://meet.google.com/x":                                  "",
	}
	for in, want := range cases {
		if got := findMeetingURL([]string{in}); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestMalformedInputNeverPanics(t *testing.T) {
	inputs := []string{
		"", "\x00\x01\x02", "BEGIN:VCALENDAR", "BEGIN:VEVENT\nEND:VEVENT", ":::", ";;;:x", "END:VEVENT\nEND:VEVENT\nEND:VALARM",
		"BEGIN:VEVENT\nDTSTART:garbage\nDTEND;TZID=:\nATTENDEE:\nORGANIZER;CN=\"unterminated:mailto:x\nEND:VEVENT",
		"BEGIN:VEVENT\nDTSTART;VALUE=DATE:2026\nEND:VEVENT", "BEGIN:VEVENT\nSUMMARY:\\\nEND:VEVENT",
		"BEGIN:VEVENT\nBEGIN:VALARM\nBEGIN:X\nEND:VEVENT", " \n \n\t\n", "BEGIN:VEVENT\nSEQUENCE:99999999999999999999\nEND:VEVENT",
		strings.Repeat("BEGIN:VEVENT\n", 100000), strings.Repeat("A", 1<<20), strings.Repeat(" x\n", 100000),
		"BEGIN:VEVENT\nSUMMARY:" + strings.Repeat("é", 10000) + "\nEND:VEVENT",
	}
	for i, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("input %d panicked: %v", i, r)
				}
			}()
			_, _ = Parse([]byte(in), nil)
		}()
	}
}

func TestSizeAndFieldCaps(t *testing.T) {
	big := "BEGIN:VEVENT\nSUMMARY:" + strings.Repeat("é", 10000) + "\nEND:VEVENT\n"
	inv, err := Parse([]byte(big), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Summary) > maxFieldLen || !isValidUTF8(inv.Summary) {
		t.Fatalf("summary len %d valid=%v", len(inv.Summary), isValidUTF8(inv.Summary))
	}
	// An event that only starts after the byte cap is never seen.
	late := strings.Repeat("X-PAD:"+strings.Repeat("a", 100)+"\n", MaxBytes/100) + "BEGIN:VEVENT\nSUMMARY:late\nEND:VEVENT\n"
	if _, err := Parse([]byte(late), nil); err != ErrNoEvent {
		t.Fatalf("want ErrNoEvent past the cap, got %v", err)
	}
}

func isValidUTF8(s string) bool { return strings.ToValidUTF8(s, "") == s }
