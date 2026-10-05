package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

type calWriteFixture struct {
	t        *testing.T
	h        *Handler
	fake     *fakeGoogleCalendar
	calID    string // writable primary calendar (local id)
	roCalID  string // reader calendar
	day      string // civil day of the seeded events
	incItems []map[string]any
	inc      int // incremental syncs served
}

func eventTimes(startDay, startClock, endClock string) map[string]any {
	return map[string]any{
		"start": map[string]any{"dateTime": startDay + "T" + startClock + "-07:00", "timeZone": "America/Los_Angeles"},
		"end":   map[string]any{"dateTime": startDay + "T" + endClock + "-07:00", "timeZone": "America/Los_Angeles"},
	}
}

func newCalWriteFixture(t *testing.T) *calWriteFixture {
	t.Helper()
	ctx := context.Background()
	f := &calWriteFixture{t: t, fake: &fakeGoogleCalendar{}}
	f.h, _ = newCalendarHandler(t, f.fake)
	now := time.Now().UTC()
	f.day = now.AddDate(0, 0, 3).Format("2006-01-02")
	seed := []map[string]any{
		{"id": "solo", "status": "confirmed", "summary": "Solo", "description": "plain"},
		{"id": "rec_inst", "recurringEventId": "rec", "status": "confirmed", "summary": "Standup",
			"organizer": map[string]any{"email": "boss@example.com"},
			"attendees": []map[string]any{
				{"email": "boss@example.com", "organizer": true, "responseStatus": "accepted"},
				{"email": "user@example.com", "self": true, "responseStatus": "needsAction"},
			}},
		{"id": "inv", "status": "confirmed", "summary": "Invite", "attendees": []map[string]any{
			{"email": "boss@example.com", "organizer": true, "responseStatus": "accepted"},
			{"email": "user@example.com", "self": true, "responseStatus": "needsAction"}}},
	}
	for i, e := range seed {
		for k, v := range eventTimes(f.day, "09:00:00", "10:00:00") {
			e[k] = v
		}
		seed[i] = e
	}
	f.fake.events = func(q url.Values) (int, map[string]any) {
		if q.Get("syncToken") == "" {
			return 200, map[string]any{"nextSyncToken": "tok-0", "items": seed}
		}
		f.inc++
		items := f.incItems
		f.incItems = nil
		return 200, map[string]any{"nextSyncToken": "tok-" + strconv.Itoa(f.inc), "items": items}
	}
	if err := f.h.SyncCalendarAccount(ctx, "acc"); err != nil {
		t.Fatal(err)
	}
	cals, _ := f.h.db.ListCalendarsForUser(ctx, "default")
	for _, c := range cals {
		if c.IsPrimary {
			f.calID = strconv.FormatInt(c.ID, 10)
		} else {
			f.roCalID = strconv.FormatInt(c.ID, 10)
		}
	}
	f.fake.write = func(r *http.Request, body map[string]any) (int, map[string]any) { return 500, nil }
	return f
}

func (f *calWriteFixture) localID(provider string) string {
	f.t.Helper()
	var id int64
	if err := f.h.db.Read().QueryRow(`SELECT id FROM calendar_events WHERE provider_event_id = ?`, provider).Scan(&id); err != nil {
		f.t.Fatalf("local id of %s: %v", provider, err)
	}
	return strconv.FormatInt(id, 10)
}

func (f *calWriteFixture) localCount(provider string) int {
	var n int
	_ = f.h.db.Read().QueryRow(`SELECT COUNT(*) FROM calendar_events WHERE provider_event_id = ?`, provider).Scan(&n)
	return n
}

func (f *calWriteFixture) call(fn http.HandlerFunc, user, method, target string, form url.Values, id string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	fn(rec, calendarReq(user, method, target, form, id))
	return rec
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("not an object: %#v", v)
	}
	return m
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct{ Error string }
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	return e.Error
}

func TestCalendarCreateTimedEventBodyAndLocalUpsert(t *testing.T) {
	f := newCalWriteFixture(t)
	f.fake.write = func(r *http.Request, body map[string]any) (int, map[string]any) {
		return 200, map[string]any{"id": "new1", "status": "confirmed", "summary": "Lunch", "htmlLink": "https://cal/x",
			"start": body["start"], "end": body["end"],
			"conferenceData": map[string]any{"entryPoints": []map[string]any{{"entryPointType": "video", "uri": "https://meet.google.com/zzz"}}}}
	}
	form := url.Values{"calendar_id": {f.calID}, "title": {" Lunch "}, "start": {f.day + "T12:00:00-07:00"}, "end": {f.day + "T13:00:00-07:00"},
		"time_zone": {"America/Los_Angeles"}, "location": {"Cafe"}, "description": {"bring cash"},
		"attendees": {"Ann <ann@example.com>, bob@example.com, ANN@example.com"}, "add_meet": {"1"},
		"recurrence": {"none"}, "reminder_minutes": {"30"}}
	rec := f.call(f.h.handleCreateCalendarEvent, "default", http.MethodPost, "/api/calendar/events", form, "")
	if rec.Code != 200 {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	w := f.fake.writeReqs[0]
	if w.Method != "POST" || w.Path != "/calendars/user@example.com/events" {
		t.Fatalf("request = %s %s", w.Method, w.Path)
	}
	if w.Query.Get("conferenceDataVersion") != "1" || w.Query.Get("sendUpdates") != "all" {
		t.Fatalf("query = %v", w.Query)
	}
	wantStart := map[string]any{"dateTime": f.day + "T12:00:00-07:00", "timeZone": "America/Los_Angeles"}
	if !reflect.DeepEqual(w.Body["start"], wantStart) || w.Body["summary"] != "Lunch" || w.Body["location"] != "Cafe" || w.Body["description"] != "bring cash" {
		t.Fatalf("body = %v", w.Body)
	}
	att, _ := json.Marshal(w.Body["attendees"])
	if string(att) != `[{"email":"ann@example.com"},{"email":"bob@example.com"}]` {
		t.Fatalf("attendees = %s", att)
	}
	if _, has := w.Body["recurrence"]; has {
		t.Fatal("recurrence none must not be sent")
	}
	req := asMap(t, asMap(t, w.Body["conferenceData"])["createRequest"])
	if req["requestId"] == "" || asMap(t, req["conferenceSolutionKey"])["type"] != "hangoutsMeet" {
		t.Fatalf("conferenceData = %v", w.Body["conferenceData"])
	}
	rem := asMap(t, w.Body["reminders"])
	if rem["useDefault"] != false || len(rem["overrides"].([]any)) != 1 {
		t.Fatalf("reminders = %v", rem)
	}
	// Local cache reflects the write immediately.
	_, got := getEvents(t, f.h, "default", f.day+"T00:00:00Z", f.day+"T23:59:59Z")
	found := false
	for _, e := range got.Events {
		found = found || (e.Title == "Lunch" && e.MeetingURL == "https://meet.google.com/zzz")
	}
	if !found {
		t.Fatalf("created event not in local store: %+v", got.Events)
	}
	// The later incremental sync re-delivers it: no duplicate, token still works.
	f.incItems = []map[string]any{{"id": "new1", "status": "confirmed", "summary": "Lunch (synced)", "start": w.Body["start"], "end": w.Body["end"]}}
	if err := f.h.SyncCalendarAccount(context.Background(), "acc"); err != nil {
		t.Fatal(err)
	}
	if n := f.localCount("new1"); n != 1 {
		t.Fatalf("rows for new1 after sync = %d", n)
	}
	if last := f.fake.eventReqs[len(f.fake.eventReqs)-1]; last.Get("syncToken") != "tok-0" {
		t.Fatalf("incremental sync lost the stored token: %v", last)
	}
}

func TestCalendarCreateAllDayAndNoGuestsNeverEmails(t *testing.T) {
	f := newCalWriteFixture(t)
	f.fake.write = func(r *http.Request, body map[string]any) (int, map[string]any) {
		return 200, map[string]any{"id": "ad1", "status": "confirmed", "summary": "Holiday", "start": body["start"], "end": body["end"]}
	}
	form := url.Values{"calendar_id": {f.calID}, "title": {"Holiday"}, "all_day": {"1"}, "start_date": {f.day}, "send_updates": {"all"}}
	rec := f.call(f.h.handleCreateCalendarEvent, "default", http.MethodPost, "/x", form, "")
	if rec.Code != 200 {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	w := f.fake.writeReqs[0]
	next := mustDay(t, f.day).AddDate(0, 0, 1).Format("2006-01-02")
	if !reflect.DeepEqual(w.Body["start"], map[string]any{"date": f.day}) || !reflect.DeepEqual(w.Body["end"], map[string]any{"date": next}) {
		t.Fatalf("all-day body = %v / %v", w.Body["start"], w.Body["end"])
	}
	if w.Query.Get("sendUpdates") != "none" || w.Query.Get("conferenceDataVersion") != "" {
		t.Fatalf("query = %v", w.Query)
	}
	if _, has := w.Body["attendees"]; has {
		t.Fatal("no attendees expected")
	}
	if n := f.localCount("ad1"); n != 1 {
		t.Fatalf("local rows = %d", n)
	}
}

func mustDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCalendarCreateRecurringTriggersIncrementalSync(t *testing.T) {
	f := newCalWriteFixture(t)
	f.fake.write = func(r *http.Request, body map[string]any) (int, map[string]any) {
		return 200, map[string]any{"id": "masterX", "status": "confirmed", "recurrence": body["recurrence"], "start": body["start"], "end": body["end"]}
	}
	f.incItems = []map[string]any{{"id": "masterX_inst1", "recurringEventId": "masterX", "status": "confirmed", "summary": "Weekly",
		"start": map[string]any{"dateTime": f.day + "T20:00:00Z"}, "end": map[string]any{"dateTime": f.day + "T21:00:00Z"}}}
	form := url.Values{"calendar_id": {f.calID}, "title": {"Weekly"}, "start": {f.day + "T13:00:00-07:00"}, "end": {f.day + "T14:00:00-07:00"},
		"time_zone": {"America/Los_Angeles"}, "recurrence": {"weekly"}}
	if rec := f.call(f.h.handleCreateCalendarEvent, "default", http.MethodPost, "/x", form, ""); rec.Code != 200 {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if got := f.fake.writeReqs[0].Body["recurrence"]; !reflect.DeepEqual(got, []any{"RRULE:FREQ=WEEKLY"}) {
		t.Fatalf("recurrence = %v", got)
	}
	if f.localCount("masterX") != 0 {
		t.Fatal("recurring master must not be stored as an instance row")
	}
	if f.localCount("masterX_inst1") != 1 || f.inc != 1 {
		t.Fatalf("instance rows = %d, incremental syncs = %d", f.localCount("masterX_inst1"), f.inc)
	}
	if last := f.fake.eventReqs[len(f.fake.eventReqs)-1]; last.Get("syncToken") != "tok-0" {
		t.Fatalf("sync after write did not use the stored token: %v", last)
	}
	// A raw RRULE is accepted.
	form.Set("recurrence", "RRULE:FREQ=WEEKLY;BYDAY=MO,WE;COUNT=10")
	if rec := f.call(f.h.handleCreateCalendarEvent, "default", http.MethodPost, "/x", form, ""); rec.Code != 200 {
		t.Fatalf("raw rrule = %d %s", rec.Code, rec.Body)
	}
}

func TestCalendarWriteValidationMakesNoGoogleCall(t *testing.T) {
	f := newCalWriteFixture(t)
	base := func() url.Values {
		return url.Values{"calendar_id": {f.calID}, "title": {"T"}, "start": {f.day + "T12:00:00Z"}, "end": {f.day + "T13:00:00Z"}, "time_zone": {"UTC"}}
	}
	many := make([]string, 101)
	for i := range many {
		many[i] = "u" + strconv.Itoa(i) + "@example.com"
	}
	cases := map[string]func(url.Values){
		"end before start":    func(v url.Values) { v.Set("end", f.day+"T11:00:00Z") },
		"end equals start":    func(v url.Values) { v.Set("end", v.Get("start")) },
		"bad start":           func(v url.Values) { v.Set("start", "tomorrow") },
		"bad email":           func(v url.Values) { v.Set("attendees", "not-an-email") },
		"too many attendees":  func(v url.Values) { v.Set("attendees", strings.Join(many, ",")) },
		"rrule with EXDATE":   func(v url.Values) { v.Set("recurrence", "RRULE:FREQ=DAILY\nEXDATE:20261010") },
		"not an rrule":        func(v url.Values) { v.Set("recurrence", "EXDATE:20261010") },
		"rrule without FREQ":  func(v url.Values) { v.Set("recurrence", "RRULE:COUNT=3") },
		"title too long":      func(v url.Values) { v.Set("title", strings.Repeat("x", 1025)) },
		"bad send_updates":    func(v url.Values) { v.Set("send_updates", "everyone") },
		"bad reminder":        func(v url.Values) { v.Set("reminder_minutes", "-5") },
		"bad time zone":       func(v url.Values) { v.Set("time_zone", "Mars/Base") },
		"all-day end":         func(v url.Values) { v.Set("all_day", "1"); v.Set("start_date", f.day); v.Set("end_date", f.day) },
		"all-day bad date":    func(v url.Values) { v.Set("all_day", "1"); v.Set("start_date", "10/05/2026") },
		"missing times":       func(v url.Values) { v.Del("start"); v.Del("end") },
		"missing calendar_id": func(v url.Values) { v.Del("calendar_id") },
	}
	for name, mutate := range cases {
		v := base()
		mutate(v)
		if rec := f.call(f.h.handleCreateCalendarEvent, "default", http.MethodPost, "/x", v, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d %s", name, rec.Code, rec.Body)
		}
	}
	if len(f.fake.writeReqs) != 0 {
		t.Fatalf("validation failures reached Google: %+v", f.fake.writeReqs)
	}
	// Exactly 100 attendees is allowed.
	f.fake.write = func(r *http.Request, body map[string]any) (int, map[string]any) {
		return 200, map[string]any{"id": "hundred", "status": "confirmed", "start": body["start"], "end": body["end"]}
	}
	v := base()
	v.Set("attendees", strings.Join(many[:100], ","))
	if rec := f.call(f.h.handleCreateCalendarEvent, "default", http.MethodPost, "/x", v, ""); rec.Code != 200 {
		t.Fatalf("100 attendees = %d %s", rec.Code, rec.Body)
	}
}

func TestCalendarWriteOwnershipAndReadOnly(t *testing.T) {
	f := newCalWriteFixture(t)
	soloID := f.localID("solo")
	form := url.Values{"calendar_id": {f.calID}, "title": {"x"}, "start": {f.day + "T12:00:00Z"}, "end": {f.day + "T13:00:00Z"}, "time_zone": {"UTC"}}
	if rec := f.call(f.h.handleCreateCalendarEvent, "attacker", http.MethodPost, "/x", form, ""); rec.Code != 404 {
		t.Fatalf("foreign create = %d", rec.Code)
	}
	if rec := f.call(f.h.handlePatchCalendarEvent, "attacker", http.MethodPatch, "/x", url.Values{"title": {"pwn"}}, soloID); rec.Code != 404 {
		t.Fatalf("foreign patch = %d", rec.Code)
	}
	if rec := f.call(f.h.handleDeleteCalendarEvent, "attacker", http.MethodDelete, "/x", nil, soloID); rec.Code != 404 {
		t.Fatalf("foreign delete = %d", rec.Code)
	}
	if rec := f.call(f.h.handleRSVPCalendarEvent, "attacker", http.MethodPost, "/x", url.Values{"response": {"accepted"}}, soloID); rec.Code != 404 {
		t.Fatalf("foreign rsvp = %d", rec.Code)
	}
	if rec := f.call(f.h.handlePatchCalendarEvent, "default", http.MethodPatch, "/x", url.Values{"title": {"x"}}, "999999"); rec.Code != 404 {
		t.Fatalf("missing patch = %d", rec.Code)
	}
	// Read-only calendar: 403 read_only from the role check, before any Google call.
	form.Set("calendar_id", f.roCalID)
	rec := f.call(f.h.handleCreateCalendarEvent, "default", http.MethodPost, "/x", form, "")
	if rec.Code != 403 || errCode(t, rec) != "read_only" {
		t.Fatalf("read-only create = %d %s", rec.Code, rec.Body)
	}
	if len(f.fake.writeReqs) != 0 {
		t.Fatalf("rejected requests reached Google: %+v", f.fake.writeReqs)
	}
	// A foreign event row is untouched.
	if f.localCount("solo") != 1 {
		t.Fatal("foreign delete removed the row")
	}
}

func TestCalendarWriteGoogleErrorsMapping(t *testing.T) {
	f := newCalWriteFixture(t)
	form := url.Values{"calendar_id": {f.calID}, "title": {"x"}, "start": {f.day + "T12:00:00Z"}, "end": {f.day + "T13:00:00Z"}, "time_zone": {"UTC"}}
	f.fake.write = func(r *http.Request, body map[string]any) (int, map[string]any) {
		return 403, map[string]any{"error": map[string]any{"code": 403, "message": "Forbidden", "errors": []map[string]any{{"reason": "forbiddenForNonOrganizer"}}}}
	}
	rec := f.call(f.h.handleCreateCalendarEvent, "default", http.MethodPost, "/x", form, "")
	if rec.Code != 403 || errCode(t, rec) != "read_only" {
		t.Fatalf("google 403 = %d %s", rec.Code, rec.Body)
	}
	f.fake.write = func(r *http.Request, body map[string]any) (int, map[string]any) {
		return 403, map[string]any{"error": map[string]any{"code": 403, "message": "Request had insufficient authentication scopes.", "errors": []map[string]any{{"reason": "insufficientPermissions"}}}}
	}
	rec = f.call(f.h.handleCreateCalendarEvent, "default", http.MethodPost, "/x", form, "")
	if rec.Code != 403 || errCode(t, rec) != "needs_reconnect" {
		t.Fatalf("scope error = %d %s", rec.Code, rec.Body)
	}
	if st, _ := f.h.db.ListCalendarAccountStates(context.Background(), "default"); !st["acc"].NeedsReconnect {
		t.Fatal("scope error did not flag the account for reconnect")
	}
	f.fake.write = func(r *http.Request, body map[string]any) (int, map[string]any) { return 503, nil }
	if rec = f.call(f.h.handleCreateCalendarEvent, "default", http.MethodPost, "/x", form, ""); rec.Code != 502 {
		t.Fatalf("google 503 = %d", rec.Code)
	}
}

func TestCalendarPatchInstanceVersusSeries(t *testing.T) {
	f := newCalWriteFixture(t)
	instID := f.localID("rec_inst")
	masterDay := mustDay(t, f.day).AddDate(0, 0, -7).Format("2006-01-02")
	f.fake.write = func(r *http.Request, body map[string]any) (int, map[string]any) {
		if r.Method == http.MethodGet {
			return 200, map[string]any{"start": map[string]any{"dateTime": masterDay + "T09:00:00-07:00", "timeZone": "America/Los_Angeles"},
				"end": map[string]any{"dateTime": masterDay + "T10:00:00-07:00", "timeZone": "America/Los_Angeles"},
				"attendees": []map[string]any{
					{"email": "boss@example.com", "organizer": true, "responseStatus": "accepted"},
					{"email": "keep@example.com", "responseStatus": "accepted"},
					{"email": "drop@example.com", "responseStatus": "declined"}}}
		}
		return 200, map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/calendars/user@example.com/events/"), "status": "confirmed", "summary": "Standup 2",
			"recurringEventId": "rec", "start": map[string]any{"dateTime": f.day + "T17:00:00Z"}, "end": map[string]any{"dateTime": f.day + "T18:00:00Z"}}
	}
	// Instance: moved 09:00-10:00 -> 10:00-11:30 PDT, single occurrence.
	form := url.Values{"scope": {"this"}, "title": {"Standup 2"}, "time_zone": {"America/Los_Angeles"},
		"start": {f.day + "T10:00:00-07:00"}, "end": {f.day + "T11:30:00-07:00"}}
	rec := f.call(f.h.handlePatchCalendarEvent, "default", http.MethodPatch, "/x", form, instID)
	if rec.Code != 200 {
		t.Fatalf("patch instance = %d %s", rec.Code, rec.Body)
	}
	w := f.fake.writeReqs[len(f.fake.writeReqs)-1]
	if w.Method != "PATCH" || w.Path != "/calendars/user@example.com/events/rec_inst" {
		t.Fatalf("instance patch targeted %s %s", w.Method, w.Path)
	}
	if st := asMap(t, w.Body["start"]); st["dateTime"] != f.day+"T10:00:00-07:00" || st["timeZone"] != "America/Los_Angeles" || st["date"] != nil {
		t.Fatalf("start = %v", st)
	}
	if _, has := st(w.Body, "date"); !has {
		t.Fatal("patch must null the unused date member")
	}
	if w.Query.Get("sendUpdates") != "all" { // the instance has guests
		t.Fatalf("sendUpdates = %q", w.Query.Get("sendUpdates"))
	}
	// Local row updated immediately.
	_, got := getEvents(t, f.h, "default", f.day+"T00:00:00Z", f.day+"T23:59:59Z")
	titles := ""
	for _, e := range got.Events {
		titles += e.Title + "|"
	}
	if !strings.Contains(titles, "Standup 2") {
		t.Fatalf("local titles = %s", titles)
	}

	// Series: patches the master, shifts its start by the same wall-clock delta
	// (09:00 -> 10:00, +30min length) on the master's own date, merges guests.
	f.fake.writeReqs = nil
	f.incItems = []map[string]any{{"id": "rec_inst", "recurringEventId": "rec", "status": "confirmed", "summary": "Standup 2",
		"start": map[string]any{"dateTime": f.day + "T17:00:00Z"}, "end": map[string]any{"dateTime": f.day + "T18:30:00Z"}}}
	form.Set("scope", "series")
	form.Set("attendees", "keep@example.com, new@example.com")
	// The stored instance now starts 10:00 PDT; move it to 11:00-12:30.
	form.Set("start", f.day+"T11:00:00-07:00")
	form.Set("end", f.day+"T12:30:00-07:00")
	if rec = f.call(f.h.handlePatchCalendarEvent, "default", http.MethodPatch, "/x", form, instID); rec.Code != 200 {
		t.Fatalf("patch series = %d %s", rec.Code, rec.Body)
	}
	if len(f.fake.writeReqs) != 2 || f.fake.writeReqs[0].Method != "GET" || f.fake.writeReqs[1].Method != "PATCH" ||
		f.fake.writeReqs[1].Path != "/calendars/user@example.com/events/rec" {
		t.Fatalf("series requests = %+v", f.fake.writeReqs)
	}
	sb := f.fake.writeReqs[1].Body
	if got := asMap(t, sb["start"])["dateTime"]; got != masterDay+"T10:00:00" {
		t.Fatalf("series start = %v, want %sT10:00:00", got, masterDay)
	}
	if got := asMap(t, sb["end"])["dateTime"]; got != masterDay+"T11:30:00" {
		t.Fatalf("series end = %v", got)
	}
	att, _ := json.Marshal(sb["attendees"])
	if string(att) != `[{"email":"boss@example.com","organizer":true,"responseStatus":"accepted"},{"email":"keep@example.com","responseStatus":"accepted"},{"email":"new@example.com"}]` {
		t.Fatalf("attendees = %s", att)
	}
	if f.inc == 0 {
		t.Fatal("series write did not trigger a calendar sync")
	}

	// Series edit that leaves the occurrence's time alone sends no start/end.
	f.fake.writeReqs = nil
	f.incItems = nil
	only := url.Values{"scope": {"series"}, "title": {"Renamed"}}
	if rec = f.call(f.h.handlePatchCalendarEvent, "default", http.MethodPatch, "/x", only, instID); rec.Code != 200 {
		t.Fatalf("title-only series = %d", rec.Code)
	}
	body := f.fake.writeReqs[len(f.fake.writeReqs)-1].Body
	if _, has := body["start"]; has || body["summary"] != "Renamed" {
		t.Fatalf("title-only body = %v", body)
	}
	// Changing the repeat rule of an existing recurring event is refused.
	if rec = f.call(f.h.handlePatchCalendarEvent, "default", http.MethodPatch, "/x", url.Values{"recurrence": {"daily"}}, instID); rec.Code != 400 {
		t.Fatalf("recurrence change on instance = %d", rec.Code)
	}
	if rec = f.call(f.h.handlePatchCalendarEvent, "default", http.MethodPatch, "/x", url.Values{"scope": {"all"}}, instID); rec.Code != 400 {
		t.Fatalf("bad scope = %d", rec.Code)
	}
}

func st(body map[string]any, member string) (any, bool) {
	m, _ := body["start"].(map[string]any)
	v, ok := m[member]
	return v, ok
}

func TestCalendarPatchSwitchToAllDayAndDescriptionPassthrough(t *testing.T) {
	f := newCalWriteFixture(t)
	f.fake.write = func(r *http.Request, body map[string]any) (int, map[string]any) {
		return 200, map[string]any{"id": "solo", "status": "confirmed", "summary": "Solo", "start": map[string]any{"date": f.day}, "end": map[string]any{"date": f.day}}
	}
	form := url.Values{"all_day": {"1"}, "start_date": {f.day}, "end_date": {mustDay(t, f.day).AddDate(0, 0, 1).Format("2006-01-02")},
		"description": {"plain"}, "attendees": {""}}
	f.fake.write = func(r *http.Request, body map[string]any) (int, map[string]any) {
		if r.Method == http.MethodGet {
			return 200, map[string]any{}
		}
		return 200, map[string]any{"id": "solo", "status": "confirmed", "summary": "Solo", "start": map[string]any{"date": f.day}, "end": map[string]any{"date": mustDay(t, f.day).AddDate(0, 0, 1).Format("2006-01-02")}}
	}
	if rec := f.call(f.h.handlePatchCalendarEvent, "default", http.MethodPatch, "/x", form, f.localID("solo")); rec.Code != 200 {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body)
	}
	body := f.fake.writeReqs[len(f.fake.writeReqs)-1]
	b, _ := json.Marshal(body.Body["start"])
	if string(b) != `{"date":"`+f.day+`","dateTime":null,"timeZone":null}` {
		t.Fatalf("all-day start = %s", b)
	}
	if _, has := body.Body["description"]; has {
		t.Fatal("unchanged description must not be written back")
	}
	if body.Query.Get("sendUpdates") != "none" {
		t.Fatalf("no guests must mean sendUpdates=none, got %q", body.Query.Get("sendUpdates"))
	}
}

func TestCalendarDeleteInstanceVersusSeries(t *testing.T) {
	f := newCalWriteFixture(t)
	f.fake.write = func(r *http.Request, body map[string]any) (int, map[string]any) { return 204, nil }
	instID := f.localID("rec_inst")
	rec := f.call(f.h.handleDeleteCalendarEvent, "default", http.MethodDelete, "/x?scope=this&send_updates=externalOnly", nil, instID)
	if rec.Code != 200 {
		t.Fatalf("delete this = %d %s", rec.Code, rec.Body)
	}
	w := f.fake.writeReqs[0]
	if w.Method != "DELETE" || w.Path != "/calendars/user@example.com/events/rec_inst" || w.Query.Get("sendUpdates") != "externalOnly" {
		t.Fatalf("delete request = %+v", w)
	}
	if f.localCount("rec_inst") != 0 {
		t.Fatal("local instance row survived delete")
	}
	// Series: re-add an instance row via sync, then delete the series.
	f.incItems = []map[string]any{{"id": "rec_inst2", "recurringEventId": "rec", "status": "confirmed", "summary": "again",
		"start": map[string]any{"dateTime": f.day + "T20:00:00Z"}, "end": map[string]any{"dateTime": f.day + "T21:00:00Z"}}}
	if err := f.h.SyncCalendarAccount(context.Background(), "acc"); err != nil {
		t.Fatal(err)
	}
	f.incItems = []map[string]any{{"id": "rec_inst2", "status": "cancelled"}}
	f.fake.writeReqs = nil
	rec = f.call(f.h.handleDeleteCalendarEvent, "default", http.MethodDelete, "/x?scope=series", nil, f.localID("rec_inst2"))
	if rec.Code != 200 {
		t.Fatalf("delete series = %d %s", rec.Code, rec.Body)
	}
	w = f.fake.writeReqs[0]
	if w.Path != "/calendars/user@example.com/events/rec" || w.Query.Get("sendUpdates") != "none" { // rec_inst2 has no guests
		t.Fatalf("series delete request = %+v", w)
	}
	if f.localCount("rec_inst2") != 0 {
		t.Fatal("series delete left the instance in the local store")
	}
	// Non-recurring delete with no guests never emails; a vanished event counts as deleted.
	f.fake.write = func(r *http.Request, body map[string]any) (int, map[string]any) { return 410, nil }
	rec = f.call(f.h.handleDeleteCalendarEvent, "default", http.MethodDelete, "/x?send_updates=all", nil, f.localID("solo"))
	if rec.Code != 200 || f.fake.writeReqs[len(f.fake.writeReqs)-1].Query.Get("sendUpdates") != "none" || f.localCount("solo") != 0 {
		t.Fatalf("solo delete = %d %+v", rec.Code, f.fake.writeReqs[len(f.fake.writeReqs)-1].Query)
	}
	if rec = f.call(f.h.handleDeleteCalendarEvent, "default", http.MethodDelete, "/x?scope=bogus", nil, f.localID("inv")); rec.Code != 400 {
		t.Fatalf("bad scope = %d", rec.Code)
	}
}

func TestCalendarRSVP(t *testing.T) {
	f := newCalWriteFixture(t)
	f.fake.write = func(r *http.Request, body map[string]any) (int, map[string]any) {
		atts := []map[string]any{{"email": "boss@example.com", "organizer": true, "responseStatus": "accepted"},
			{"email": "user@example.com", "self": true, "responseStatus": "needsAction"}}
		if r.Method == http.MethodGet {
			return 200, map[string]any{"attendees": atts}
		}
		atts[1]["responseStatus"] = "tentative"
		return 200, map[string]any{"id": "inv", "status": "confirmed", "summary": "Invite", "attendees": atts,
			"start": map[string]any{"dateTime": f.day + "T16:00:00Z"}, "end": map[string]any{"dateTime": f.day + "T17:00:00Z"}}
	}
	id := f.localID("inv")
	if rec := f.call(f.h.handleRSVPCalendarEvent, "default", http.MethodPost, "/x", url.Values{"response": {"maybe"}}, id); rec.Code != 400 {
		t.Fatalf("bad response = %d", rec.Code)
	}
	if rec := f.call(f.h.handleRSVPCalendarEvent, "default", http.MethodPost, "/x", url.Values{"response": {"tentative"}}, f.localID("solo")); rec.Code != 400 {
		t.Fatalf("rsvp on event without self attendee = %d", rec.Code)
	}
	if len(f.fake.writeReqs) != 0 {
		t.Fatal("rejected rsvp reached Google")
	}
	rec := f.call(f.h.handleRSVPCalendarEvent, "default", http.MethodPost, "/x", url.Values{"response": {"tentative"}}, id)
	if rec.Code != 200 {
		t.Fatalf("rsvp = %d %s", rec.Code, rec.Body)
	}
	patch := f.fake.writeReqs[1]
	if patch.Method != "PATCH" || patch.Path != "/calendars/user@example.com/events/inv" || patch.Query.Get("sendUpdates") != "all" {
		t.Fatalf("rsvp patch = %+v", patch)
	}
	b, _ := json.Marshal(patch.Body["attendees"])
	if string(b) != `[{"email":"boss@example.com","organizer":true,"responseStatus":"accepted"},{"email":"user@example.com","responseStatus":"tentative","self":true}]` {
		t.Fatalf("attendees = %s", b)
	}
	_, got := getEvents(t, f.h, "default", f.day+"T00:00:00Z", f.day+"T23:59:59Z")
	for _, e := range got.Events {
		if e.Title == "Invite" && e.SelfResponse != "tentative" {
			t.Fatalf("local self_response = %q", e.SelfResponse)
		}
	}
}
