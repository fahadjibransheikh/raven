package ical

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func vcal(events ...string) []byte {
	return crlf("BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//x//EN\n" + strings.Join(events, "\n") + "\nEND:VCALENDAR\n")
}

func vevent(lines ...string) string {
	return "BEGIN:VEVENT\nUID:u1\nDTSTAMP:20260101T000000Z\n" + strings.Join(lines, "\n") + "\nEND:VEVENT"
}

func expand(t *testing.T, data []byte, from, to string) ([]Instance, error) {
	t.Helper()
	r, err := ParseResource(data, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := time.Parse("2006-01-02", from)
	e, _ := time.Parse("2006-01-02", to)
	return r.Instances(f, e)
}

func starts(in []Instance) []string {
	var out []string
	for _, i := range in {
		out = append(out, i.Start.UTC().Format("2006-01-02T15:04Z"))
	}
	return out
}

func eq(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

func TestExpandWeeklyAcrossDST(t *testing.T) {
	// 2026-03-08 is the US spring-forward day: 09:00 local is 17:00Z before, 16:00Z after.
	in, err := expand(t, vcal(vevent("DTSTART;TZID=America/Los_Angeles:20260302T090000", "DTEND;TZID=America/Los_Angeles:20260302T100000", "RRULE:FREQ=WEEKLY")), "2026-03-01", "2026-03-25")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, starts(in), "2026-03-02T17:00Z", "2026-03-09T16:00Z", "2026-03-16T16:00Z", "2026-03-23T16:00Z")
	if d := in[1].End.Sub(in[1].Start); d != time.Hour {
		t.Fatalf("duration %v", d)
	}
}

func TestExpandMonthlySecondTuesday(t *testing.T) {
	in, err := expand(t, vcal(vevent("DTSTART:20261013T140000Z", "DTEND:20261013T150000Z", "RRULE:FREQ=MONTHLY;BYDAY=2TU")), "2026-10-01", "2027-01-31")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, starts(in), "2026-10-13T14:00Z", "2026-11-10T14:00Z", "2026-12-08T14:00Z", "2027-01-12T14:00Z")
}

func TestExpandMonthlyLastDayAndNegativeOrdinal(t *testing.T) {
	in, _ := expand(t, vcal(vevent("DTSTART;VALUE=DATE:20260131", "RRULE:FREQ=MONTHLY;BYMONTHDAY=-1;COUNT=4")), "2026-01-01", "2027-01-01")
	eq(t, starts(in), "2026-01-31T00:00Z", "2026-02-28T00:00Z", "2026-03-31T00:00Z", "2026-04-30T00:00Z")
	in, _ = expand(t, vcal(vevent("DTSTART:20261030T100000Z", "RRULE:FREQ=MONTHLY;BYDAY=-1FR;COUNT=2")), "2026-01-01", "2027-01-01")
	eq(t, starts(in), "2026-10-30T10:00Z", "2026-11-27T10:00Z")
}

func TestExpandMonthlyDay31SkipsShortMonths(t *testing.T) {
	in, _ := expand(t, vcal(vevent("DTSTART:20260131T100000Z", "RRULE:FREQ=MONTHLY;COUNT=3")), "2026-01-01", "2027-01-01")
	eq(t, starts(in), "2026-01-31T10:00Z", "2026-03-31T10:00Z", "2026-05-31T10:00Z")
}

func TestExpandCountAndUntil(t *testing.T) {
	in, _ := expand(t, vcal(vevent("DTSTART:20261001T100000Z", "RRULE:FREQ=DAILY;COUNT=3")), "2026-09-01", "2027-01-01")
	eq(t, starts(in), "2026-10-01T10:00Z", "2026-10-02T10:00Z", "2026-10-03T10:00Z")
	in, _ = expand(t, vcal(vevent("DTSTART:20261001T100000Z", "RRULE:FREQ=DAILY;INTERVAL=2;UNTIL=20261007T100000Z")), "2026-09-01", "2027-01-01")
	eq(t, starts(in), "2026-10-01T10:00Z", "2026-10-03T10:00Z", "2026-10-05T10:00Z", "2026-10-07T10:00Z")
	// a DATE until includes that whole local day
	in, _ = expand(t, vcal(vevent("DTSTART:20261001T100000Z", "RRULE:FREQ=DAILY;UNTIL=20261002")), "2026-09-01", "2027-01-01")
	eq(t, starts(in), "2026-10-01T10:00Z", "2026-10-02T10:00Z")
}

func TestExpandWeeklyByDayInterval(t *testing.T) {
	// Mon 2026-10-05, every other week on Mon+Wed.
	in, _ := expand(t, vcal(vevent("DTSTART:20261005T100000Z", "RRULE:FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE;COUNT=5")), "2026-10-01", "2027-01-01")
	eq(t, starts(in), "2026-10-05T10:00Z", "2026-10-07T10:00Z", "2026-10-19T10:00Z", "2026-10-21T10:00Z", "2026-11-02T10:00Z")
}

func TestExpandYearlyByMonth(t *testing.T) {
	in, _ := expand(t, vcal(vevent("DTSTART;VALUE=DATE:20260704", "RRULE:FREQ=YEARLY;BYMONTH=7;BYMONTHDAY=4")), "2026-01-01", "2029-01-01")
	eq(t, starts(in), "2026-07-04T00:00Z", "2027-07-04T00:00Z", "2028-07-04T00:00Z")
	if !in[0].Event.AllDay {
		t.Fatal("want all-day")
	}
}

func TestExpandExdateAndOverride(t *testing.T) {
	master := vevent("DTSTART;TZID=America/Los_Angeles:20261005T090000", "DTEND;TZID=America/Los_Angeles:20261005T100000",
		"RRULE:FREQ=DAILY;COUNT=4", "EXDATE;TZID=America/Los_Angeles:20261006T090000", "SUMMARY:Standup")
	override := "BEGIN:VEVENT\nUID:u1\nDTSTAMP:20260101T000000Z\nRECURRENCE-ID;TZID=America/Los_Angeles:20261007T090000\n" +
		"DTSTART;TZID=America/Los_Angeles:20261007T130000\nDTEND;TZID=America/Los_Angeles:20261007T140000\nSUMMARY:Moved\nEND:VEVENT"
	in, err := expand(t, vcal(master, override), "2026-10-01", "2026-11-01")
	if err != nil {
		t.Fatal(err)
	}
	// Oct 6 excluded; Oct 7 replaced by the 13:00 override (20:00Z, PDT); count includes the excluded one.
	eq(t, starts(in), "2026-10-05T16:00Z", "2026-10-07T20:00Z", "2026-10-08T16:00Z")
	if !in[1].Override || in[1].Event.Summary != "Moved" || in[1].RecurrenceID.UTC().Format("2006-01-02T15:04Z") != "2026-10-07T16:00Z" {
		t.Fatalf("override instance = %+v", in[1])
	}
	if in[0].Event.Summary != "Standup" || in[0].Override {
		t.Fatalf("master instance = %+v", in[0])
	}
}

func TestExpandOverrideMovedIntoWindow(t *testing.T) {
	master := vevent("DTSTART:20260901T100000Z", "DTEND:20260901T110000Z", "RRULE:FREQ=WEEKLY")
	override := "BEGIN:VEVENT\nUID:u1\nRECURRENCE-ID:20260908T100000Z\nDTSTART:20261021T100000Z\nDTEND:20261021T110000Z\nEND:VEVENT"
	in, _ := expand(t, vcal(master, override), "2026-10-18", "2026-10-25")
	eq(t, starts(in), "2026-10-20T10:00Z", "2026-10-21T10:00Z")
}

func TestExpandCancelledOverrideAndEventSkipped(t *testing.T) {
	master := vevent("DTSTART:20261001T100000Z", "RRULE:FREQ=DAILY;COUNT=3")
	override := "BEGIN:VEVENT\nUID:u1\nRECURRENCE-ID:20261002T100000Z\nDTSTART:20261002T100000Z\nSTATUS:CANCELLED\nEND:VEVENT"
	in, _ := expand(t, vcal(master, override), "2026-10-01", "2026-11-01")
	eq(t, starts(in), "2026-10-01T10:00Z", "2026-10-03T10:00Z")
}

func TestExpandUnsupportedRuleKeepsFirstOccurrence(t *testing.T) {
	for _, rule := range []string{"FREQ=MONTHLY;BYDAY=MO,TU;BYSETPOS=-1", "FREQ=HOURLY", "FREQ=WEEKLY;COUNT=2;UNTIL=20270101", "FREQ=YEARLY;BYDAY=20MO"} {
		in, err := expand(t, vcal(vevent("DTSTART:20261001T100000Z", "RRULE:"+rule)), "2026-01-01", "2027-12-31")
		if !errors.Is(err, ErrUnsupportedRRule) {
			t.Fatalf("%s: err = %v", rule, err)
		}
		eq(t, starts(in), "2026-10-01T10:00Z")
		if in[0].RecurrenceID.IsZero() {
			t.Fatalf("%s: first occurrence needs a recurrence id so its key stays stable", rule)
		}
	}
}

func TestExpandWindowFiltersAndNonRecurring(t *testing.T) {
	in, _ := expand(t, vcal(vevent("DTSTART:20261001T100000Z", "DTEND:20261001T110000Z")), "2026-10-01", "2026-10-02")
	if len(in) != 1 || !in[0].RecurrenceID.IsZero() {
		t.Fatalf("one-off = %+v", in)
	}
	in, _ = expand(t, vcal(vevent("DTSTART:20261001T100000Z", "DTEND:20261001T110000Z")), "2026-10-02", "2026-10-03")
	if len(in) != 0 {
		t.Fatalf("outside window = %+v", in)
	}
	in, _ = expand(t, vcal(vevent("DTSTART:20260101T100000Z", "RRULE:FREQ=DAILY")), "2026-10-10", "2026-10-12")
	eq(t, starts(in), "2026-10-10T10:00Z", "2026-10-11T10:00Z")
}

func TestExpandNeverMatchingRuleTerminates(t *testing.T) {
	in, _ := expand(t, vcal(vevent("DTSTART:20260101T100000Z", "RRULE:FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30")), "2026-01-01", "2030-01-01")
	eq(t, starts(in), "2026-01-01T10:00Z")
}

func TestDocRoundTripKeepsUnknownPropsAndFolds(t *testing.T) {
	long := "Description with a comma, a semicolon; a backslash \\ and newline\nand " + strings.Repeat("é", 100)
	src := crlf("BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:u1\nX-APPLE-STRUCTURED-LOCATION;VALUE=URI;X-TITLE=\"a:b\":geo:1,2\nBEGIN:VALARM\nTRIGGER:-PT15M\nEND:VALARM\nEND:VEVENT\nEND:VCALENDAR\n")
	root, err := ParseDoc(src)
	if err != nil {
		t.Fatal(err)
	}
	root.Children[0].Set("DESCRIPTION", EscapeText(long))
	out := string(root.Bytes())
	for _, want := range []string{"X-APPLE-STRUCTURED-LOCATION;VALUE=URI;X-TITLE=\"a:b\":geo:1,2\r\n", "BEGIN:VALARM\r\nTRIGGER:-PT15M\r\nEND:VALARM\r\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("lost %q in\n%s", want, out)
		}
	}
	for _, ln := range strings.Split(out, "\r\n") {
		if len(ln) > 75 {
			t.Fatalf("line of %d octets: %q", len(ln), ln)
		}
	}
	if strings.Contains(strings.ReplaceAll(out, "\r\n", ""), "\n") {
		t.Fatal("bare newline")
	}
	back, err := ParseResource([]byte(out), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.Events[0].Description; got != long {
		t.Fatalf("description round trip = %q", got)
	}
}

func TestParseDocRejectsBrokenInput(t *testing.T) {
	for _, in := range []string{"", "BEGIN:VEVENT\nEND:VEVENT\n", "BEGIN:VCALENDAR\nBEGIN:VEVENT\nEND:VCALENDAR\n", "BEGIN:VCALENDAR\n"} {
		if _, err := ParseDoc([]byte(in)); err == nil {
			t.Fatalf("%q parsed", in)
		}
	}
	if _, err := ParseDoc(make([]byte, MaxDocBytes+1)); err == nil {
		t.Fatal("oversize parsed")
	}
}

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{"PT1H30M": 90 * time.Minute, "P1D": 24 * time.Hour, "P1W": 168 * time.Hour, "-PT5M": -5 * time.Minute} {
		if got, ok := parseDuration(in); !ok || got != want {
			t.Fatalf("%s = %v %v", in, got, ok)
		}
	}
	if _, ok := parseDuration("P1Y"); ok {
		t.Fatal("P1Y accepted")
	}
	r, _ := ParseResource(vcal(vevent("DTSTART:20261001T100000Z", "DURATION:PT45M")), time.UTC)
	if d := r.Events[0].End.Sub(r.Events[0].Start); d != 45*time.Minute {
		t.Fatalf("duration end = %v", d)
	}
}

func TestTimeProp(t *testing.T) {
	ts := time.Date(2026, 3, 9, 16, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		allDay    bool
		tz        string
		head, val string
	}{{false, "", "DTSTART", "20260309T160000Z"}, {false, "America/Los_Angeles", "DTSTART;TZID=America/Los_Angeles", "20260309T090000"}, {true, "x", "DTSTART;VALUE=DATE", "20260309"}} {
		h, v := TimeProp("DTSTART", ts, c.allDay, c.tz)
		if h != c.head || v != c.val {
			t.Fatalf("%+v -> %s:%s", c, h, v)
		}
	}
}
