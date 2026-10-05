package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/cristianadrielbraun/gofer/internal/auth"
	"github.com/cristianadrielbraun/gofer/internal/mailauth"
	"github.com/cristianadrielbraun/gofer/internal/models"
	"github.com/cristianadrielbraun/gofer/internal/providers"
	"github.com/cristianadrielbraun/gofer/internal/storage"
)

// fakeGraph is a minimal Microsoft Graph: every request is recorded and answered by fn.
type fakeGraph struct {
	mu   sync.Mutex
	fn   func(r *http.Request, body map[string]any) (int, any)
	reqs []graphReq
}

type graphReq struct {
	Method, Path string
	Query        url.Values
	Prefer       string
	Auth         string
	Body         map[string]any
}

func (f *fakeGraph) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.reqs = append(f.reqs, graphReq{r.Method, r.URL.Path, r.URL.Query(), r.Header.Get("Prefer"), r.Header.Get("Authorization"), body})
	code, out := f.fn(r, body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if out != nil {
		_ = json.NewEncoder(w).Encode(out)
	}
}

func (f *fakeGraph) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func (f *fakeGraph) last() graphReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reqs[len(f.reqs)-1]
}

const outlookCalScope = "https://graph.microsoft.com/Calendars.ReadWrite"

// newOutlookCalendarHandler seeds an outlook account "oacc" (person@outlook.com)
// whose stored grant carries scopes, and points Graph at the fake. tokenURL is
// the OAuth token endpoint used if a refresh is needed.
func newOutlookCalendarHandler(t *testing.T, fake *fakeGraph, scopes, tokenURL string) (*Handler, *storage.DB) {
	t.Helper()
	ctx := context.Background()
	h, db := newGmailAPITestHandler(t, ctx)
	if _, err := db.Write().ExecContext(ctx, `
		INSERT INTO accounts (id, user_id, provider, provider_account_id, email_address)
		VALUES ('oacc', 'default', ?, 'ms-subject', 'person@outlook.com')`, providers.ProviderOutlook); err != nil {
		t.Fatal(err)
	}
	manager := mailauth.New(&mailauth.Config{MicrosoftClient: &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: tokenURL}}}, db, testMailboxCredentialKey)
	expires := time.Now().Add(time.Hour)
	if err := manager.UpsertOAuthAccount(ctx, "oacc", providers.OAuthMicrosoft, "ms-subject", "graph-token", "refresh-token", "Bearer", &expires, scopes); err != nil {
		t.Fatal(err)
	}
	h.mailboxAuth = manager
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO users (id, username, username_normalized, name) VALUES ('attacker','attacker','attacker','A')`); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	prev := outlookGraphBaseURL
	outlookGraphBaseURL = server.URL
	t.Cleanup(func() { outlookGraphBaseURL = prev })
	prevSleep := outlookCalendarSleep
	outlookCalendarSleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { outlookCalendarSleep = prevSleep })
	return h, db
}

func graphCalendarsPayload() map[string]any {
	return map[string]any{"value": []map[string]any{
		{"id": "cal-main", "name": "Calendar", "color": "auto", "hexColor": "", "canEdit": true, "isDefaultCalendar": true, "owner": map[string]any{"address": "person@outlook.com"}},
		{"id": "cal-shared", "name": "Team", "color": "lightGreen", "hexColor": "#00aa00", "canEdit": true, "owner": map[string]any{"address": "boss@example.com"}},
		{"id": "cal-ro", "name": "Holidays", "color": "lightBlue", "canEdit": false, "owner": map[string]any{"address": "boss@example.com"}},
	}}
}

func TestOutlookCalendarListMapping(t *testing.T) {
	ctx := context.Background()
	fake := &fakeGraph{fn: func(r *http.Request, _ map[string]any) (int, any) {
		if r.URL.Path == "/me/calendars" {
			return 200, graphCalendarsPayload()
		}
		return 200, map[string]any{"value": []any{}, "@odata.deltaLink": outlookGraphBaseURL + "/me/calendars/x/calendarView/delta?$deltatoken=t"}
	}}
	h, db := newOutlookCalendarHandler(t, fake, outlookCalScope, "http://unused")
	if err := h.SyncCalendarAccount(ctx, "oacc"); err != nil {
		t.Fatal(err)
	}
	cals, err := db.ListCalendarsForUser(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]models.Calendar{}
	for _, c := range cals {
		if c.AccountID == "oacc" {
			got[c.ProviderCalendarID] = c
		}
	}
	if len(got) != 3 {
		t.Fatalf("calendars = %+v", cals)
	}
	if c := got["cal-main"]; c.AccessRole != "owner" || !c.IsPrimary || c.Color != "#0078d4" || c.Provider != "outlook" || !c.Selected {
		t.Errorf("main = %+v", c)
	}
	if c := got["cal-shared"]; c.AccessRole != "writer" || c.Color != "#00aa00" || c.Selected {
		t.Errorf("shared = %+v", c)
	}
	if c := got["cal-ro"]; c.AccessRole != "reader" || c.Color != "#71afe5" {
		t.Errorf("read-only = %+v", c)
	}
	// Only the selected calendar's events were requested, with UTC times asked for.
	deltas := 0
	for _, rq := range fake.reqs {
		if rq.Auth != "Bearer graph-token" {
			t.Errorf("auth = %q", rq.Auth)
		}
		if !strings.Contains(rq.Prefer, `outlook.timezone="UTC"`) {
			t.Errorf("Prefer = %q", rq.Prefer)
		}
		if strings.HasSuffix(rq.Path, "/calendarView/delta") {
			deltas++
			if rq.Path != "/me/calendars/cal-main/calendarView/delta" || rq.Query.Get("startDateTime") == "" || rq.Query.Get("endDateTime") == "" {
				t.Errorf("delta request = %+v", rq)
			}
		}
	}
	if deltas != 1 {
		t.Errorf("delta requests = %d, want 1 (selected calendar only)", deltas)
	}
	// The JSON API reports the provider.
	rec := httptest.NewRecorder()
	h.handleListCalendars(rec, calendarReq("default", http.MethodGet, "/api/calendar/calendars", nil, ""))
	if !strings.Contains(rec.Body.String(), `"provider":"outlook"`) {
		t.Fatalf("calendars JSON lacks provider: %s", rec.Body)
	}
}

func TestOutlookDeltaFullIncrementalRemovedAndExpiry(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	d1 := now.AddDate(0, 0, 2).Truncate(24 * time.Hour)
	day := func(d time.Time) string { return d.Format("2006-01-02") }
	timed := func(id, title string, h int) map[string]any {
		return map[string]any{"id": id, "type": "singleInstance", "subject": title, "iCalUId": "u-" + id,
			"start":                 map[string]any{"dateTime": day(d1) + "T" + fmt.Sprintf("%02d", h) + ":00:00.0000000", "timeZone": "UTC"},
			"end":                   map[string]any{"dateTime": day(d1) + "T" + fmt.Sprintf("%02d", h+1) + ":30:00.0000000", "timeZone": "UTC"},
			"originalStartTimeZone": "Pacific Standard Time", "webLink": "https://outlook.live.com/owa/?itemid=" + id,
			"body": map[string]any{"contentType": "html", "content": "<p>Agenda &amp; notes</p>"}, "location": map[string]any{"displayName": "Room 1"},
			"organizer":      map[string]any{"emailAddress": map[string]any{"address": "boss@example.com", "name": "Boss"}},
			"attendees":      []map[string]any{{"type": "required", "status": map[string]any{"response": "tentativelyAccepted"}, "emailAddress": map[string]any{"address": "Person@Outlook.com"}}},
			"responseStatus": map[string]any{"response": "tentativelyAccepted"},
			"onlineMeeting":  map[string]any{"joinUrl": "https://teams.example/join"}, "lastModifiedDateTime": "2026-10-01T10:00:00Z"}
	}
	allDay := map[string]any{"id": "e-allday", "type": "singleInstance", "subject": "Offsite", "isAllDay": true, "isOrganizer": true,
		"start": map[string]any{"dateTime": day(d1.AddDate(0, 0, 1)) + "T00:00:00.0000000", "timeZone": "UTC"},
		"end":   map[string]any{"dateTime": day(d1.AddDate(0, 0, 3)) + "T00:00:00.0000000", "timeZone": "UTC"}}
	base := func(q string) string { return outlookGraphBaseURL + "/me/calendars/cal-main/calendarView/delta?" + q }
	phase := "full"
	var deltaCalls []string
	fake := &fakeGraph{}
	fake.fn = func(r *http.Request, _ map[string]any) (int, any) {
		if r.URL.Path == "/me/calendars" {
			return 200, map[string]any{"value": graphCalendarsPayload()["value"].([]map[string]any)[:1]}
		}
		deltaCalls = append(deltaCalls, phase+" "+r.URL.RawQuery)
		q := r.URL.Query()
		switch phase {
		case "full":
			if q.Get("$skiptoken") == "" {
				if q.Get("startDateTime") == "" || q.Get("endDateTime") == "" {
					t.Errorf("full sync without window: %s", r.URL.RawQuery)
				}
				return 200, map[string]any{"@odata.nextLink": base("$skiptoken=p2"), "value": []any{timed("e1", "Standup", 9)}}
			}
			return 200, map[string]any{"@odata.deltaLink": base("$deltatoken=d1"), "value": []any{
				allDay, map[string]any{"id": "outside", "@removed": map[string]any{"reason": "deleted"}},
				map[string]any{"id": "cancelled", "isCancelled": true, "subject": "Canceled: x", "start": map[string]any{"dateTime": "2026-01-01T00:00:00", "timeZone": "UTC"}, "end": map[string]any{"dateTime": "2026-01-01T01:00:00", "timeZone": "UTC"}}}}
		case "incremental":
			if q.Get("$deltatoken") != "d1" || q.Get("startDateTime") != "" {
				t.Errorf("incremental must replay the stored deltaLink only: %s", r.URL.RawQuery)
			}
			return 200, map[string]any{"@odata.deltaLink": base("$deltatoken=d2"), "value": []any{
				map[string]any{"id": "e-allday", "@removed": map[string]any{"reason": "deleted"}}, timed("e3", "New", 5)}}
		case "gone":
			if q.Get("$deltatoken") != "" {
				return http.StatusGone, map[string]any{"error": map[string]any{"code": "syncStateNotFound", "message": "gone"}}
			}
			return 200, map[string]any{"@odata.deltaLink": base("$deltatoken=d3"), "value": []any{timed("e9", "After resync", 7)}}
		case "notfound":
			if q.Get("$deltatoken") != "" {
				return http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "SyncStateNotFound"}}
			}
			return 200, map[string]any{"@odata.deltaLink": base("$deltatoken=d4"), "value": []any{timed("e10", "After 400 resync", 7)}}
		}
		return 500, nil
	}
	h, db := newOutlookCalendarHandler(t, fake, outlookCalScope, "http://unused")
	token := func() string {
		var s string
		_ = db.Read().QueryRow(`SELECT sync_token FROM calendars WHERE provider_calendar_id = 'cal-main'`).Scan(&s)
		return s
	}
	titles := func() map[string]models.CalendarEvent {
		code, got := getEvents(t, h, "default", now.AddDate(0, 0, -1).Format(time.RFC3339), now.AddDate(0, 0, 10).Format(time.RFC3339))
		if code != 200 {
			t.Fatalf("events = %d", code)
		}
		out := map[string]models.CalendarEvent{}
		for _, e := range got.Events {
			out[e.Title] = models.CalendarEvent{Title: e.Title, AllDay: e.AllDay, StartDate: e.StartDate, EndDate: e.EndDate, MeetingURL: e.MeetingURL, SelfResponse: e.SelfResponse, Description: e.Description}
		}
		return out
	}

	if err := h.SyncCalendarAccount(ctx, "oacc"); err != nil {
		t.Fatalf("full sync: %v", err)
	}
	if token() != base("$deltatoken=d1") {
		t.Fatalf("stored deltaLink = %q", token())
	}
	got := titles()
	if len(got) != 2 {
		t.Fatalf("after full sync: %+v (cancelled and @removed rows must not be stored)", got)
	}
	if e := got["Standup"]; e.MeetingURL != "https://teams.example/join" || e.SelfResponse != "tentative" || e.Description != "Agenda & notes" {
		t.Errorf("timed = %+v", e)
	}
	if e := got["Offsite"]; !e.AllDay || e.StartDate != day(d1.AddDate(0, 0, 1)) || e.EndDate != day(d1.AddDate(0, 0, 3)) {
		t.Errorf("all-day = %+v", e)
	}
	var startAt string
	_ = db.Read().QueryRow(`SELECT start_at FROM calendar_events WHERE provider_event_id = 'e1'`).Scan(&startAt)
	if !strings.HasPrefix(startAt, day(d1)+"T09:00:00") && !strings.HasPrefix(startAt, day(d1)+" 09:00:00") {
		t.Errorf("stored start_at = %q, want 09:00 UTC on %s", startAt, day(d1))
	}
	var tz string
	_ = db.Read().QueryRow(`SELECT event_time_zone FROM calendar_events WHERE provider_event_id = 'e1'`).Scan(&tz)
	if tz != "" {
		t.Errorf("a Windows zone name must not be stored as an IANA event zone, got %q", tz)
	}

	phase = "incremental"
	if err := h.SyncCalendarAccount(ctx, "oacc"); err != nil {
		t.Fatalf("incremental: %v", err)
	}
	got = titles()
	if _, ok := got["Offsite"]; ok || len(got) != 2 || got["New"].Title == "" {
		t.Fatalf("after incremental (@removed e-allday, add e3) = %+v", got)
	}
	if token() != base("$deltatoken=d2") {
		t.Fatalf("token = %q", token())
	}

	for _, c := range []struct{ phase, want, wantToken string }{
		{"gone", "After resync", base("$deltatoken=d3")},
		{"notfound", "After 400 resync", base("$deltatoken=d4")},
	} {
		phase = c.phase
		if err := h.SyncCalendarAccount(ctx, "oacc"); err != nil {
			t.Fatalf("%s resync: %v", c.phase, err)
		}
		got = titles()
		if len(got) != 1 || got[c.want].Title == "" || token() != c.wantToken {
			t.Fatalf("after %s: events %+v token %q", c.phase, got, token())
		}
	}
	states, _ := db.ListCalendarAccountStates(ctx, "default")
	if states["oacc"].NeedsReconnect || states["oacc"].LastError != "" {
		t.Fatalf("state = %+v", states["oacc"])
	}
}

func TestOutlookEventToModelAllDayAndUTC(t *testing.T) {
	// Midnight-in-zone all-day, multi-day: end is exclusive as delivered.
	g := graphEvent{ID: "a", IsAllDay: true,
		Start: graphDateTime{"2026-10-05T00:00:00.0000000", "UTC"}, End: graphDateTime{"2026-10-07T00:00:00.0000000", "UTC"}}
	e, err := outlookEventToModel(g, "me@x.com")
	if err != nil || !e.AllDay || e.StartDate != "2026-10-05" || e.EndDate != "2026-10-07" {
		t.Fatalf("all-day = %+v, %v", e, err)
	}
	// Zero-length all-day input still spans a day.
	g.End = g.Start
	if e, _ = outlookEventToModel(g, ""); e.EndDate != "2026-10-06" {
		t.Fatalf("zero-length all-day end = %q", e.EndDate)
	}
	// An all-day event Graph renders as Pacific midnight (07:00Z) still lands on its own date.
	g = graphEvent{ID: "b", IsAllDay: true,
		Start: graphDateTime{"2026-10-05T07:00:00.0000000", "UTC"}, End: graphDateTime{"2026-10-06T07:00:00.0000000", "UTC"}}
	if e, _ = outlookEventToModel(g, ""); e.StartDate != "2026-10-05" || e.EndDate != "2026-10-06" {
		t.Fatalf("shifted all-day = %+v", e)
	}
	// Timed events are normalised to UTC even if Graph answers in another zone.
	g = graphEvent{ID: "c", Start: graphDateTime{"2026-10-07T09:00:00.0000000", "America/Los_Angeles"}, End: graphDateTime{"2026-10-07T10:00:00.0000000", "America/Los_Angeles"}}
	if e, err = outlookEventToModel(g, ""); err != nil || e.StartAt.UTC().Format(time.RFC3339) != "2026-10-07T16:00:00Z" || e.AllDay {
		t.Fatalf("timed = %+v, %v", e, err)
	}
	// Occurrences link to their series; single instances do not; organizer is listed with guests.
	g = graphEvent{ID: "d", Type: "occurrence", SeriesMasterID: "m1", Start: graphDateTime{"2026-10-07T09:00:00", "UTC"}, End: graphDateTime{"2026-10-07T10:00:00", "UTC"}}
	g.Organizer.EmailAddress = graphEmail{Address: "boss@x.com"}
	g.Attendees = append(g.Attendees, struct {
		Type         string     `json:"type"`
		EmailAddress graphEmail `json:"emailAddress"`
		Status       struct {
			Response string `json:"response"`
		} `json:"status"`
	}{Type: "optional", EmailAddress: graphEmail{Address: "me@x.com"}})
	e, _ = outlookEventToModel(g, "ME@x.com")
	if e.RecurringEventID != "m1" || len(e.Attendees) != 2 || !e.Attendees[0].Organizer || !e.Attendees[1].Self || !e.Attendees[1].Optional || e.SelfResponse != "needsAction" {
		t.Fatalf("occurrence = %+v", e)
	}
}

func TestOutlookRateLimitRetriesAfterRetryAfter(t *testing.T) {
	ctx := context.Background()
	hits := 0
	fake := &fakeGraph{}
	fake.fn = func(r *http.Request, _ map[string]any) (int, any) {
		if r.URL.Path == "/me/calendars" {
			hits++
			if hits == 1 {
				return http.StatusTooManyRequests, map[string]any{"error": map[string]any{"code": "TooManyRequests"}}
			}
			return 200, map[string]any{"value": []any{}}
		}
		return 500, nil
	}
	h, _ := newOutlookCalendarHandler(t, fake, outlookCalScope, "http://unused")
	var waited time.Duration
	outlookCalendarSleep = func(_ context.Context, d time.Duration) error { waited = d; return nil }
	// Retry-After is honoured via a header-setting wrapper around the fake.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		fake.ServeHTTP(rec, r)
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		if rec.Code == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "7")
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}))
	defer srv.Close()
	outlookGraphBaseURL = srv.URL
	if err := h.SyncCalendarAccount(ctx, "oacc"); err != nil {
		t.Fatalf("sync after one 429: %v", err)
	}
	if hits != 2 || waited < 5*time.Second || waited > 7*time.Second {
		t.Fatalf("hits=%d waited=%v, want one retry after ~7s", hits, waited)
	}
}

func TestOutlookCalendarMissingScopeFlagsReconnect(t *testing.T) {
	ctx := context.Background()
	fake := &fakeGraph{fn: func(*http.Request, map[string]any) (int, any) { return 200, map[string]any{"value": []any{}} }}
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"AADSTS65001: not consented"}`))
	}))
	defer tokenSrv.Close()
	// A grant that predates the calendar scope (mail/contacts only).
	h, db := newOutlookCalendarHandler(t, fake, "https://graph.microsoft.com/Mail.ReadWrite https://graph.microsoft.com/Contacts.ReadWrite", tokenSrv.URL)
	err := h.SyncCalendarAccount(ctx, "oacc")
	if !isOutlookCalendarScopeError(err) {
		t.Fatalf("err = %v, want calendar scope error", err)
	}
	if fake.count() != 0 {
		t.Fatalf("Graph was called %d times without a calendar token", fake.count())
	}
	st, _ := db.ListCalendarAccountStates(ctx, "default")
	if !st["oacc"].NeedsReconnect {
		t.Fatalf("state = %+v, want needs_reconnect", st["oacc"])
	}
	rec := httptest.NewRecorder()
	h.handleSyncCalendars(rec, calendarReq("default", http.MethodPost, "/api/calendar/sync", url.Values{}, ""))
	if !strings.Contains(rec.Body.String(), "needs_reconnect") {
		t.Fatalf("sync response = %s", rec.Body)
	}
	// A write is refused the same way, without touching Graph.
	cal := seedOutlookCalendar(t, db, "cal-main", "owner")
	rec = httptest.NewRecorder()
	h.handleCreateCalendarEvent(rec, calendarReq("default", http.MethodPost, "/x", timedCreateForm(cal.ID), ""))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "needs_reconnect") || fake.count() != 0 {
		t.Fatalf("create = %d %s (graph calls %d)", rec.Code, rec.Body, fake.count())
	}
}

// ---------- writes ----------

func seedOutlookCalendar(t *testing.T, db *storage.DB, providerID, role string) models.Calendar {
	t.Helper()
	ctx := context.Background()
	if err := db.UpsertCalendars(ctx, "oacc", []models.Calendar{{ProviderCalendarID: providerID, Name: providerID, AccessRole: role, IsPrimary: role == "owner"}}); err != nil {
		t.Fatal(err)
	}
	cals, _ := db.ListCalendarsForUser(ctx, "default")
	for _, c := range cals {
		if c.AccountID == "oacc" && c.ProviderCalendarID == providerID {
			return c
		}
	}
	t.Fatal("calendar not seeded")
	return models.Calendar{}
}

func seedOutlookEvent(t *testing.T, db *storage.DB, cal models.Calendar, e models.CalendarEvent) int64 {
	t.Helper()
	if e.StartAt.IsZero() {
		e.StartAt = time.Date(2026, 10, 7, 16, 0, 0, 0, time.UTC)
		e.EndAt = e.StartAt.Add(time.Hour)
	}
	if e.Status == "" {
		e.Status = "confirmed"
	}
	if err := db.ApplyCalendarEventPage(context.Background(), cal.ID, cal.AccountID, []models.CalendarEvent{e}, nil); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := db.Read().QueryRow(`SELECT id FROM calendar_events WHERE calendar_id = ? AND provider_event_id = ?`, cal.ID, e.ProviderEventID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func timedCreateForm(calID int64) url.Values {
	return url.Values{
		"calendar_id": {strconv.FormatInt(calID, 10)}, "title": {"Lunch"}, "location": {"Bar"}, "description": {"Menu"},
		"start": {"2026-10-07T09:00:00-07:00"}, "end": {"2026-10-07T10:30:00-07:00"}, "time_zone": {"America/Los_Angeles"},
	}
}

func writtenGraphEvent(id string, over map[string]any) map[string]any {
	e := map[string]any{"id": id, "type": "singleInstance", "subject": "Lunch", "webLink": "https://outlook.live.com/x",
		"start": map[string]any{"dateTime": "2026-10-07T16:00:00.0000000", "timeZone": "UTC"},
		"end":   map[string]any{"dateTime": "2026-10-07T17:30:00.0000000", "timeZone": "UTC"}}
	for k, v := range over {
		e[k] = v
	}
	return e
}

func TestOutlookCreateRequestBodies(t *testing.T) {
	ctx := context.Background()
	providers := []string{"unknown"}
	fake := &fakeGraph{}
	fake.fn = func(r *http.Request, body map[string]any) (int, any) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/me/calendars/cal-main":
			return 200, map[string]any{"allowedOnlineMeetingProviders": providers}
		case r.Method == http.MethodPost && r.URL.Path == "/me/calendars/cal-main/events":
			if body["recurrence"] != nil {
				return 201, writtenGraphEvent("master-1", map[string]any{"type": "seriesMaster", "recurrence": body["recurrence"]})
			}
			over := map[string]any{}
			if body["isOnlineMeeting"] == true {
				over["onlineMeeting"] = map[string]any{"joinUrl": "https://teams.example/j"}
			}
			return 201, writtenGraphEvent("new-1", over)
		}
		return 200, map[string]any{"value": []any{}, "@odata.deltaLink": outlookGraphBaseURL + "/me/calendars/cal-main/calendarView/delta?$deltatoken=z"}
	}
	h, db := newOutlookCalendarHandler(t, fake, outlookCalScope, "http://unused")
	cal := seedOutlookCalendar(t, db, "cal-main", "owner")
	post := func(form url.Values) (int, map[string]any) {
		rec := httptest.NewRecorder()
		h.handleCreateCalendarEvent(rec, calendarReq("default", http.MethodPost, "/api/calendar/events", form, ""))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	find := func(method, path string) graphReq {
		for i := len(fake.reqs) - 1; i >= 0; i-- {
			if fake.reqs[i].Method == method && fake.reqs[i].Path == path {
				return fake.reqs[i]
			}
		}
		t.Fatalf("no %s %s in %+v", method, path, fake.reqs)
		return graphReq{}
	}

	// Timed one-off event with a guest and a reminder; personal calendar cannot host a meeting.
	form := timedCreateForm(cal.ID)
	form.Set("attendees", "g@example.com")
	form.Set("reminder_minutes", "30")
	form.Set("add_meet", "1")
	form.Set("send_updates", "none") // accepted, but Graph always sends invitations
	code, out := post(form)
	if code != 200 || out["provider_event_id"] != "new-1" || out["synced"] != true || out["warning"] == nil {
		t.Fatalf("create = %d %+v", code, out)
	}
	b := find(http.MethodPost, "/me/calendars/cal-main/events").Body
	if b["subject"] != "Lunch" || b["isAllDay"] != false || b["isReminderOn"] != true || b["reminderMinutesBeforeStart"] != float64(30) {
		t.Errorf("body = %+v", b)
	}
	if s := b["start"].(map[string]any); s["dateTime"] != "2026-10-07T16:00:00" || s["timeZone"] != "UTC" {
		t.Errorf("start must be UTC: %+v", s)
	}
	if b["isOnlineMeeting"] != nil {
		t.Errorf("personal calendar must not request an online meeting: %+v", b)
	}
	att := b["attendees"].([]any)[0].(map[string]any)
	if att["emailAddress"].(map[string]any)["address"] != "g@example.com" || att["type"] != "required" {
		t.Errorf("attendees = %+v", b["attendees"])
	}
	var stored int
	_ = db.Read().QueryRow(`SELECT COUNT(*) FROM calendar_events WHERE calendar_id = ? AND provider_event_id = 'new-1'`, cal.ID).Scan(&stored)
	if stored != 1 {
		t.Errorf("written event not stored locally")
	}

	// Work account: Teams is offered, so add_meet becomes an online meeting.
	providers = []string{"teamsForBusiness"}
	form = timedCreateForm(cal.ID)
	form.Set("add_meet", "1")
	code, out = post(form)
	b = find(http.MethodPost, "/me/calendars/cal-main/events").Body
	if code != 200 || out["warning"] != nil || b["isOnlineMeeting"] != true || b["onlineMeetingProvider"] != "teamsForBusiness" {
		t.Fatalf("teams create = %d %+v body %+v", code, out, b)
	}

	// All-day, weekly recurring: a patternedRecurrence with noEnd on the start date.
	form = url.Values{"calendar_id": {strconv.FormatInt(cal.ID, 10)}, "title": {"Hol"}, "all_day": {"1"}, "start_date": {"2026-10-07"}, "end_date": {"2026-10-08"}, "recurrence": {"weekly"}}
	code, out = post(form)
	if code != 200 || out["provider_event_id"] != "master-1" {
		t.Fatalf("recurring create = %d %+v", code, out)
	}
	b = find(http.MethodPost, "/me/calendars/cal-main/events").Body
	rec := b["recurrence"].(map[string]any)
	pat, rng := rec["pattern"].(map[string]any), rec["range"].(map[string]any)
	if pat["type"] != "weekly" || pat["interval"] != float64(1) || pat["daysOfWeek"].([]any)[0] != "wednesday" || pat["firstDayOfWeek"] != "sunday" ||
		rng["type"] != "noEnd" || rng["startDate"] != "2026-10-07" {
		t.Errorf("recurrence = %+v", rec)
	}
	if s := b["start"].(map[string]any); b["isAllDay"] != true || s["dateTime"] != "2026-10-07T00:00:00" || b["end"].(map[string]any)["dateTime"] != "2026-10-08T00:00:00" {
		t.Errorf("all-day body = %+v", b)
	}
	// A recurring timed event keeps wall-clock time in the user's zone so it follows DST.
	form = timedCreateForm(cal.ID)
	form.Set("recurrence", "monthly")
	post(form)
	b = find(http.MethodPost, "/me/calendars/cal-main/events").Body
	if s := b["start"].(map[string]any); s["dateTime"] != "2026-10-07T09:00:00" || s["timeZone"] != "America/Los_Angeles" {
		t.Errorf("recurring start = %+v", s)
	}
	if p := b["recurrence"].(map[string]any)["pattern"].(map[string]any); p["type"] != "absoluteMonthly" || p["dayOfMonth"] != float64(7) {
		t.Errorf("monthly pattern = %+v", p)
	}

	// Raw RRULEs and read-only calendars are refused before any Graph write.
	before := fake.count()
	form = timedCreateForm(cal.ID)
	form.Set("recurrence", "RRULE:FREQ=WEEKLY;COUNT=3")
	if code, _ = post(form); code != http.StatusBadRequest || fake.count() != before {
		t.Errorf("raw RRULE = %d (graph calls +%d)", code, fake.count()-before)
	}
	ro := seedOutlookCalendar(t, db, "cal-ro", "reader")
	if code, _ = post(timedCreateForm(ro.ID)); code != http.StatusForbidden || fake.count() != before {
		t.Errorf("read-only = %d", code)
	}
	_ = ctx
}

func TestOutlookPatchDeleteRSVPTargeting(t *testing.T) {
	fake := &fakeGraph{}
	fake.fn = func(r *http.Request, body map[string]any) (int, any) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/me/events/master-1":
			return 200, writtenGraphEvent("master-1", map[string]any{"type": "seriesMaster", "start": map[string]any{"dateTime": "2026-09-30T16:00:00.0000000", "timeZone": "UTC"},
				"end": map[string]any{"dateTime": "2026-09-30T17:00:00.0000000", "timeZone": "UTC"}})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/me/events/"):
			return 200, writtenGraphEvent(strings.TrimPrefix(r.URL.Path, "/me/events/"), map[string]any{"type": "exception", "seriesMasterId": "master-1", "attendees": []map[string]any{
				{"type": "required", "status": map[string]any{"response": "accepted"}, "emailAddress": map[string]any{"address": "keep@example.com"}},
				{"type": "required", "emailAddress": map[string]any{"address": "drop@example.com"}},
				{"type": "resource", "emailAddress": map[string]any{"address": "room@example.com"}}}})
		case r.Method == http.MethodPatch:
			return 200, writtenGraphEvent(strings.TrimPrefix(r.URL.Path, "/me/events/"), map[string]any{"subject": "Renamed", "type": "exception", "seriesMasterId": "master-1"})
		case r.Method == http.MethodDelete:
			return 204, nil
		case r.Method == http.MethodPost:
			return 202, nil
		}
		return 200, map[string]any{"value": []any{}, "@odata.deltaLink": outlookGraphBaseURL + "/me/calendars/cal-main/calendarView/delta?$deltatoken=z"}
	}
	h, db := newOutlookCalendarHandler(t, fake, outlookCalScope, "http://unused")
	cal := seedOutlookCalendar(t, db, "cal-main", "owner")
	occ := seedOutlookEvent(t, db, cal, models.CalendarEvent{ProviderEventID: "occ-1", RecurringEventID: "master-1", Title: "Weekly", OrganizerEmail: "boss@example.com", EventTimeZone: "",
		Attendees: []models.EventAttendee{{Email: "boss@example.com", Organizer: true}, {Email: "person@outlook.com", Self: true, Response: "needsAction"}}, SelfResponse: "needsAction"})
	call := func(handler func(http.ResponseWriter, *http.Request), method, target string, form url.Values, id int64, user string) (int, string) {
		rec := httptest.NewRecorder()
		req := calendarReq(user, method, target, form, strconv.FormatInt(id, 10))
		handler(rec, req)
		return rec.Code, rec.Body.String()
	}
	reqsAfter := func(n int) []graphReq { return fake.reqs[n:] }

	// PATCH this occurrence: addresses the occurrence id, UTC times, replace-all attendees merged.
	n := fake.count()
	code, body := call(h.handlePatchCalendarEvent, http.MethodPatch, "/x", url.Values{
		"scope": {"this"}, "title": {"Renamed"}, "start": {"2026-10-07T10:00:00-07:00"}, "end": {"2026-10-07T11:00:00-07:00"}, "time_zone": {"America/Los_Angeles"},
		"attendees": {"keep@example.com,new@example.com"}}, occ, "default")
	if code != 200 {
		t.Fatalf("patch this = %d %s", code, body)
	}
	var patch graphReq
	for _, r := range reqsAfter(n) {
		if r.Method == http.MethodPatch {
			patch = r
		}
	}
	if patch.Path != "/me/events/occ-1" || patch.Body["subject"] != "Renamed" || patch.Body["start"].(map[string]any)["dateTime"] != "2026-10-07T17:00:00" || patch.Body["start"].(map[string]any)["timeZone"] != "UTC" {
		t.Errorf("patch this = %+v", patch)
	}
	var addrs []string
	for _, a := range patch.Body["attendees"].([]any) {
		addrs = append(addrs, a.(map[string]any)["emailAddress"].(map[string]any)["address"].(string))
	}
	if strings.Join(addrs, ",") != "room@example.com,keep@example.com,new@example.com" {
		t.Errorf("merged attendees = %v (resource kept, unlisted dropped)", addrs)
	}

	// PATCH series: addresses the master, shifting its own wall-clock start by the same amount (+1h from 9:00 to 10:00 local).
	n = fake.count()
	code, body = call(h.handlePatchCalendarEvent, http.MethodPatch, "/x", url.Values{
		"scope": {"series"}, "start": {"2026-10-07T10:00:00-07:00"}, "end": {"2026-10-07T11:00:00-07:00"}, "time_zone": {"America/Los_Angeles"}}, occ, "default")
	if code != 200 {
		t.Fatalf("patch series = %d %s", code, body)
	}
	patch = graphReq{}
	for _, r := range reqsAfter(n) {
		if r.Method == http.MethodPatch {
			patch = r
		}
	}
	if patch.Path != "/me/events/master-1" || patch.Body["start"].(map[string]any)["dateTime"] != "2026-09-30T10:00:00" || patch.Body["start"].(map[string]any)["timeZone"] != "America/Los_Angeles" {
		t.Errorf("patch series = %+v", patch)
	}

	// DELETE this vs series. (The series patch above re-synced and emptied the cache; reseed.)
	occ = seedOutlookEvent(t, db, cal, models.CalendarEvent{ProviderEventID: "occ-1", RecurringEventID: "master-1", Title: "Weekly",
		Attendees: []models.EventAttendee{{Email: "person@outlook.com", Self: true}}, SelfResponse: "needsAction", OrganizerEmail: "boss@example.com"})
	n = fake.count()
	if code, body = call(h.handleDeleteCalendarEvent, http.MethodDelete, "/x?scope=this", nil, occ, "default"); code != 200 {
		t.Fatalf("delete this = %d %s", code, body)
	}
	if r := reqsAfter(n)[0]; r.Method != http.MethodDelete || r.Path != "/me/events/occ-1" {
		t.Errorf("delete this = %+v", r)
	}
	var left int
	_ = db.Read().QueryRow(`SELECT COUNT(*) FROM calendar_events WHERE provider_event_id = 'occ-1'`).Scan(&left)
	if left != 0 {
		t.Error("deleted occurrence still cached")
	}
	occ = seedOutlookEvent(t, db, cal, models.CalendarEvent{ProviderEventID: "occ-2", RecurringEventID: "master-1", Title: "Weekly",
		Attendees: []models.EventAttendee{{Email: "person@outlook.com", Self: true}}, SelfResponse: "needsAction", OrganizerEmail: "boss@example.com"})
	n = fake.count()
	if code, body = call(h.handleDeleteCalendarEvent, http.MethodDelete, "/x?scope=series", nil, occ, "default"); code != 200 {
		t.Fatalf("delete series = %d %s", code, body)
	}
	if r := reqsAfter(n)[0]; r.Method != http.MethodDelete || r.Path != "/me/events/master-1" {
		t.Errorf("delete series = %+v", r)
	}

	// RSVP: accept/decline/tentativelyAccept on the occurrence or the master, sendResponse true.
	occ = seedOutlookEvent(t, db, cal, models.CalendarEvent{ProviderEventID: "occ-3", RecurringEventID: "master-1", Title: "Weekly", OrganizerEmail: "boss@example.com",
		Attendees: []models.EventAttendee{{Email: "boss@example.com", Organizer: true}, {Email: "person@outlook.com", Self: true}}, SelfResponse: "needsAction"})
	for _, c := range []struct{ response, scope, wantPath string }{
		{"accepted", "this", "/me/events/occ-3/accept"}, {"declined", "this", "/me/events/occ-3/decline"},
		{"tentative", "series", "/me/events/master-1/tentativelyAccept"},
	} {
		n = fake.count()
		code, body = call(h.handleRSVPCalendarEvent, http.MethodPost, "/x", url.Values{"response": {c.response}, "scope": {c.scope}}, occ, "default")
		if code != 200 {
			t.Fatalf("rsvp %s = %d %s", c.response, code, body)
		}
		if r := reqsAfter(n)[0]; r.Method != http.MethodPost || r.Path != c.wantPath || r.Body["sendResponse"] != true {
			t.Errorf("rsvp %s = %+v", c.response, r)
		}
	}
	// The organizer cannot answer their own meeting; no Graph call is made.
	single := seedOutlookEvent(t, db, cal, models.CalendarEvent{ProviderEventID: "one-1", Title: "Once", OrganizerEmail: "person@outlook.com", SelfResponse: "accepted",
		Attendees: []models.EventAttendee{{Email: "person@outlook.com", Self: true, Organizer: true}, {Email: "g@example.com"}}})
	n = fake.count()
	if code, _ = call(h.handleRSVPCalendarEvent, http.MethodPost, "/x", url.Values{"response": {"accepted"}}, single, "default"); code != http.StatusBadRequest || fake.count() != n {
		t.Errorf("organizer rsvp = %d (graph calls +%d)", code, fake.count()-n)
	}
}

func TestOutlookWritesRejectForeignUserWithoutGraphCall(t *testing.T) {
	fake := &fakeGraph{fn: func(*http.Request, map[string]any) (int, any) { return 200, map[string]any{} }}
	h, db := newOutlookCalendarHandler(t, fake, outlookCalScope, "http://unused")
	cal := seedOutlookCalendar(t, db, "cal-main", "owner")
	ev := seedOutlookEvent(t, db, cal, models.CalendarEvent{ProviderEventID: "e1", Title: "Mine",
		Attendees: []models.EventAttendee{{Email: "person@outlook.com", Self: true}}})
	idStr := strconv.FormatInt(ev, 10)
	for name, run := range map[string]func(*httptest.ResponseRecorder){
		"create": func(rec *httptest.ResponseRecorder) {
			h.handleCreateCalendarEvent(rec, calendarReq("attacker", http.MethodPost, "/x", timedCreateForm(cal.ID), ""))
		},
		"patch": func(rec *httptest.ResponseRecorder) {
			h.handlePatchCalendarEvent(rec, calendarReq("attacker", http.MethodPatch, "/x", url.Values{"title": {"x"}}, idStr))
		},
		"delete": func(rec *httptest.ResponseRecorder) {
			h.handleDeleteCalendarEvent(rec, calendarReq("attacker", http.MethodDelete, "/x", nil, idStr))
		},
		"rsvp": func(rec *httptest.ResponseRecorder) {
			h.handleRSVPCalendarEvent(rec, calendarReq("attacker", http.MethodPost, "/x", url.Values{"response": {"accepted"}}, idStr))
		},
	} {
		rec := httptest.NewRecorder()
		run(rec)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s as foreign user = %d, want 404", name, rec.Code)
		}
	}
	if fake.count() != 0 {
		t.Fatalf("foreign requests reached Graph %d times", fake.count())
	}
	_ = auth.User{}
}

func TestOutlookCalendarList403FlagsReconnect(t *testing.T) {
	ctx := context.Background()
	fake := &fakeGraph{fn: func(*http.Request, map[string]any) (int, any) {
		return http.StatusForbidden, map[string]any{"error": map[string]any{"code": "ErrorAccessDenied"}}
	}}
	h, db := newOutlookCalendarHandler(t, fake, outlookCalScope, "http://unused")
	if err := h.SyncCalendarAccount(ctx, "oacc"); !isOutlookCalendarScopeError(err) {
		t.Fatalf("err = %v, want calendar scope error", err)
	}
	if st, _ := db.ListCalendarAccountStates(ctx, "default"); !st["oacc"].NeedsReconnect {
		t.Fatalf("state = %+v, want needs_reconnect", st["oacc"])
	}
}

func titlesOf(t *testing.T, db *storage.DB, calID int64) map[string]string { // provider id -> "title|recurring"
	t.Helper()
	rows, err := db.Read().Query(`SELECT provider_event_id, title, recurring_event_id FROM calendar_events WHERE calendar_id = ?`, calID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, title, rec string
		_ = rows.Scan(&id, &title, &rec)
		out[id] = title + "|" + rec
	}
	return out
}

// Live shape: delta delivers occurrences with only id/type/seriesMasterId/start/end.
func TestOutlookDeltaOccurrencesAreFilledFromSeriesMaster(t *testing.T) {
	ctx := context.Background()
	d := time.Now().UTC().AddDate(0, 0, 3).Truncate(24 * time.Hour).Format("2006-01-02")
	minimal := func(id, typ, hh string, over map[string]any) map[string]any {
		e := map[string]any{"id": id, "type": typ, "seriesMasterId": "master-1",
			"start": map[string]any{"dateTime": d + "T" + hh + ":00:00.0000000", "timeZone": "UTC"},
			"end":   map[string]any{"dateTime": d + "T" + hh + ":30:00.0000000", "timeZone": "UTC"}}
		for k, v := range over {
			e[k] = v
		}
		return e
	}
	masterGets := 0
	fake := &fakeGraph{}
	fake.fn = func(r *http.Request, _ map[string]any) (int, any) {
		switch {
		case r.URL.Path == "/me/calendars":
			return 200, map[string]any{"value": graphCalendarsPayload()["value"].([]map[string]any)[:1]}
		case r.URL.Path == "/me/events/master-1":
			masterGets++
			return 200, map[string]any{"id": "master-1", "type": "seriesMaster", "subject": "Weekly sync", "location": map[string]any{"displayName": "Room 9"},
				"organizer":     map[string]any{"emailAddress": map[string]any{"address": "boss@example.com"}},
				"attendees":     []map[string]any{{"type": "required", "emailAddress": map[string]any{"address": "person@outlook.com"}, "status": map[string]any{"response": "accepted"}}},
				"onlineMeeting": map[string]any{"joinUrl": "https://teams.example/j"}, "webLink": "https://outlook.live.com/m", "responseStatus": map[string]any{"response": "accepted"}}
		}
		return 200, map[string]any{"@odata.deltaLink": outlookGraphBaseURL + "/me/calendars/cal-main/calendarView/delta?$deltatoken=d", "value": []any{
			minimal("occ-1", "occurrence", "09", nil), minimal("occ-2", "occurrence", "10", nil),
			minimal("exc-1", "exception", "11", map[string]any{"subject": "Moved sync"})}}
	}
	h, db := newOutlookCalendarHandler(t, fake, outlookCalScope, "http://unused")
	if err := h.SyncCalendarAccount(ctx, "oacc"); err != nil {
		t.Fatal(err)
	}
	cal := seedOutlookCalendar(t, db, "cal-main", "owner")
	got := titlesOf(t, db, cal.ID)
	if got["occ-1"] != "Weekly sync|master-1" || got["occ-2"] != "Weekly sync|master-1" || got["exc-1"] != "Moved sync|master-1" {
		t.Fatalf("rows = %+v", got)
	}
	if masterGets != 1 {
		t.Errorf("series master fetched %d times, want once per sync", masterGets)
	}
	var loc, meet, att string
	_ = db.Read().QueryRow(`SELECT location, meeting_url, attendees FROM calendar_events WHERE provider_event_id = 'occ-1'`).Scan(&loc, &meet, &att)
	if loc != "Room 9" || meet != "https://teams.example/j" || !strings.Contains(att, "boss@example.com") {
		t.Errorf("occurrence location=%q meeting=%q attendees=%s", loc, meet, att)
	}
}

// Live shape: the create response has no "type", only a recurrence.
func TestOutlookRecurringCreateStoresInstancesNotTheMaster(t *testing.T) {
	fake := &fakeGraph{}
	fake.fn = func(r *http.Request, body map[string]any) (int, any) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/me/calendars/cal-main/events":
			e := writtenGraphEvent("master-1", map[string]any{"recurrence": body["recurrence"]})
			delete(e, "type")
			return 201, e
		case r.URL.Path == "/me/events/master-1/instances":
			if r.URL.Query().Get("startDateTime") == "" || r.URL.Query().Get("endDateTime") == "" {
				t.Errorf("instances without window: %s", r.URL.RawQuery)
			}
			var v []any
			for i, hh := range []string{"16", "17"} {
				v = append(v, map[string]any{"id": "inst-" + strconv.Itoa(i), "type": "occurrence", "seriesMasterId": "master-1", "subject": "Lunch",
					"start": map[string]any{"dateTime": "2026-10-2" + strconv.Itoa(7+i) + "T" + hh + ":00:00.0000000", "timeZone": "UTC"},
					"end":   map[string]any{"dateTime": "2026-10-2" + strconv.Itoa(7+i) + "T" + hh + ":30:00.0000000", "timeZone": "UTC"}})
			}
			return 200, map[string]any{"value": v}
		case r.URL.Path == "/me/events/master-1":
			return 200, map[string]any{"id": "master-1", "type": "seriesMaster", "subject": "Lunch"}
		}
		return 500, nil
	}
	h, db := newOutlookCalendarHandler(t, fake, outlookCalScope, "http://unused")
	cal := seedOutlookCalendar(t, db, "cal-main", "owner")
	form := timedCreateForm(cal.ID)
	form.Set("recurrence", "weekly")
	rec := httptest.NewRecorder()
	h.handleCreateCalendarEvent(rec, calendarReq("default", http.MethodPost, "/x", form, ""))
	if rec.Code != 200 {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	got := titlesOf(t, db, cal.ID)
	if _, ok := got["master-1"]; ok || len(got) != 2 || got["inst-0"] != "Lunch|master-1" {
		t.Fatalf("rows = %+v, want only the two instances", got)
	}
}

func TestOutlookSeriesDeleteClearsLocalRowsAndReportsFailure(t *testing.T) {
	status := http.StatusNoContent
	fake := &fakeGraph{}
	fake.fn = func(r *http.Request, _ map[string]any) (int, any) {
		if r.Method == http.MethodDelete {
			if status != http.StatusNoContent {
				return status, map[string]any{"error": map[string]any{"code": "ErrorAccessDenied"}}
			}
			return 204, nil
		}
		return 200, map[string]any{"value": []any{}, "@odata.deltaLink": outlookGraphBaseURL + "/me/calendars/cal-main/calendarView/delta?$deltatoken=z"}
	}
	h, db := newOutlookCalendarHandler(t, fake, outlookCalScope, "http://unused")
	cal := seedOutlookCalendar(t, db, "cal-main", "owner")
	seed := func() (occ, master int64) {
		for i := 0; i < 5; i++ {
			id := seedOutlookEvent(t, db, cal, models.CalendarEvent{ProviderEventID: "inst-" + strconv.Itoa(i), RecurringEventID: "master-1", Title: "W"})
			if i == 0 {
				occ = id
			}
		}
		seedOutlookEvent(t, db, cal, models.CalendarEvent{ProviderEventID: "other", Title: "Other"})
		// A series master wrongly cached as a one-off row (the earlier bug).
		master = seedOutlookEvent(t, db, cal, models.CalendarEvent{ProviderEventID: "master-1", Title: "W"})
		return
	}
	del := func(id int64, q string) (int, string) {
		rec := httptest.NewRecorder()
		h.handleDeleteCalendarEvent(rec, calendarReq("default", http.MethodDelete, "/x?"+q, nil, strconv.FormatInt(id, 10)))
		return rec.Code, rec.Body.String()
	}
	count := func() int { return len(titlesOf(t, db, cal.ID)) }

	occ, master := seed()
	status = http.StatusForbidden
	if code, _ := del(occ, "scope=series"); code == 200 || count() != 7 {
		t.Fatalf("failed Graph delete must be an error and keep rows: code=%d rows=%d", code, count())
	}
	status = http.StatusNoContent
	if code, body := del(occ, "scope=series"); code != 200 || count() != 1 {
		t.Fatalf("series delete = %d %s, rows left %d (want only 'other')", code, body, count())
	}
	if last := fake.last(); last.Method != http.MethodDelete || last.Path != "/me/events/master-1" {
		t.Fatalf("graph delete = %+v, want the series master", last)
	}
	// Series scope on the stray master row itself.
	_, master = seed()
	if code, body := del(master, "scope=series"); code != 200 || count() != 1 {
		t.Fatalf("delete via master row = %d %s, rows left %d", code, body, count())
	}
}
