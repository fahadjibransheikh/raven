package handler

import (
	"context"
	"encoding/json"
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
	"github.com/cristianadrielbraun/gofer/internal/providers"
	"github.com/cristianadrielbraun/gofer/internal/storage"
)

// fakeGoogleCalendar serves calendarList and per-calendar events. Each events
// request is recorded so tests can assert which parameters were sent.
type fakeGoogleCalendar struct {
	mu        sync.Mutex
	listCode  int // non-zero: calendarList answers this status with an insufficientPermissions body
	events    func(q url.Values) (int, map[string]any)
	eventReqs []url.Values
	// write answers every request that is not calendarList or an events list
	// (insert/get/patch/delete); each call is recorded in writeReqs.
	write     func(r *http.Request, body map[string]any) (int, map[string]any)
	writeReqs []recordedWrite
}

type recordedWrite struct {
	Method string
	Path   string
	Query  url.Values
	Body   map[string]any
}

func (f *fakeGoogleCalendar) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer gmail-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/users/me/calendarList":
		if f.listCode != 0 {
			w.WriteHeader(f.listCode)
			_, _ = w.Write([]byte(`{"error":{"code":403,"message":"Request had insufficient authentication scopes.","errors":[{"reason":"insufficientPermissions"}],"status":"PERMISSION_DENIED"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
			{"id": "user@example.com", "summary": "Me", "backgroundColor": "#9fe1e7", "timeZone": "America/Los_Angeles", "accessRole": "owner", "primary": true},
			{"id": "holidays", "summary": "Holidays", "accessRole": "reader"},
		}})
	case f.write != nil && strings.HasPrefix(r.URL.Path, "/calendars/") && (r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/events")):
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.writeReqs = append(f.writeReqs, recordedWrite{r.Method, r.URL.Path, r.URL.Query(), body})
		code, out := f.write(r, body)
		w.WriteHeader(code)
		if out != nil {
			_ = json.NewEncoder(w).Encode(out)
		}
	case strings.HasSuffix(r.URL.Path, "/events"):
		if !strings.Contains(r.URL.Path, "user@example.com") {
			w.WriteHeader(http.StatusInternalServerError) // unselected calendar must not be fetched
			return
		}
		f.eventReqs = append(f.eventReqs, r.URL.Query())
		code, body := f.events(r.URL.Query())
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newCalendarHandler(t *testing.T, fake *fakeGoogleCalendar) (*Handler, *storage.DB) {
	t.Helper()
	ctx := context.Background()
	h, db := newGmailAPITestHandler(t, ctx)
	manager := mailauth.New(&mailauth.Config{GoogleClient: &oauth2.Config{}}, db, testMailboxCredentialKey)
	expires := time.Now().Add(time.Hour)
	if err := manager.UpsertOAuthAccount(ctx, "acc", providers.OAuthGoogle, "google-subject", "gmail-token", "refresh-token", "Bearer", &expires, "https://mail.google.com/"); err != nil {
		t.Fatal(err)
	}
	h.mailboxAuth = manager
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO users (id, username, username_normalized, name) VALUES ('attacker','attacker','attacker','A')`); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	prev := googleCalendarAPIBaseURL
	googleCalendarAPIBaseURL = server.URL
	t.Cleanup(func() { googleCalendarAPIBaseURL = prev })
	return h, db
}

func calendarReq(userID, method, target string, form url.Values, pathID string) *http.Request {
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	if pathID != "" {
		req.SetPathValue("id", pathID)
	}
	return req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{ID: userID, Username: userID}))
}

type eventsResp struct {
	Events []struct {
		Title         string `json:"title"`
		Start         string `json:"start"`
		End           string `json:"end"`
		AllDay        bool   `json:"all_day"`
		StartDate     string `json:"start_date"`
		EndDate       string `json:"end_date"`
		CalendarColor string `json:"calendar_color"`
		MeetingURL    string `json:"meeting_url"`
		SelfResponse  string `json:"self_response"`
		Description   string `json:"description"`
	} `json:"events"`
}

func getEvents(t *testing.T, h *Handler, userID, start, end string) (int, eventsResp) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.handleListCalendarEvents(rec, calendarReq(userID, http.MethodGet, "/api/calendar/events?start="+url.QueryEscape(start)+"&end="+url.QueryEscape(end), nil, ""))
	var out eventsResp
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, out
}

func TestGoogleCalendarFullIncrementalGoneAndCancelled(t *testing.T) {
	ctx := context.Background()
	fake := &fakeGoogleCalendar{}
	h, db := newCalendarHandler(t, fake)
	now := time.Now().UTC()
	day := now.AddDate(0, 0, 3).Format("2006-01-02")
	dayAfter := now.AddDate(0, 0, 4).Format("2006-01-02")
	timedStart := now.AddDate(0, 0, 2).Truncate(24 * time.Hour)
	laStart := timedStart.Format("2006-01-02") + "T09:00:00-07:00" // 16:00Z
	laEnd := timedStart.Format("2006-01-02") + "T10:00:00-07:00"
	phase := "full"
	fake.events = func(q url.Values) (int, map[string]any) {
		switch phase {
		case "full":
			if q.Get("syncToken") != "" || q.Get("timeMin") == "" || q.Get("timeMax") == "" || q.Get("singleEvents") != "true" {
				t.Errorf("full sync params = %v", q)
			}
			if q.Get("pageToken") == "" {
				return 200, map[string]any{"nextPageToken": "p2", "items": []map[string]any{{
					"id": "e1", "status": "confirmed", "summary": "Standup", "iCalUID": "u1",
					"description": "<p>Agenda &amp; notes</p>", "hangoutLink": "https://meet.google.com/abc",
					"start":     map[string]any{"dateTime": laStart, "timeZone": "America/Los_Angeles"},
					"end":       map[string]any{"dateTime": laEnd, "timeZone": "America/Los_Angeles"},
					"attendees": []map[string]any{{"email": "user@example.com", "self": true, "responseStatus": "tentative"}},
				}}}
			}
			return 200, map[string]any{"nextSyncToken": "sync-1", "items": []map[string]any{
				{"id": "e2", "status": "confirmed", "summary": "Offsite", "start": map[string]any{"date": day}, "end": map[string]any{"date": dayAfter}},
				{"id": "gone", "status": "cancelled"},
			}}
		case "incremental":
			if q.Get("syncToken") != "sync-1" {
				t.Errorf("syncToken = %q", q.Get("syncToken"))
			}
			for _, banned := range []string{"timeMin", "timeMax", "updatedMin", "orderBy", "q"} {
				if q.Get(banned) != "" {
					t.Errorf("incremental sync sent %s", banned)
				}
			}
			return 200, map[string]any{"nextSyncToken": "sync-2", "items": []map[string]any{
				{"id": "e2", "status": "cancelled"},
				{"id": "e3", "status": "confirmed", "summary": "New", "start": map[string]any{"dateTime": laStart}, "end": map[string]any{"dateTime": laEnd}},
			}}
		case "gone":
			if q.Get("syncToken") != "" {
				return http.StatusGone, map[string]any{"error": map[string]any{"code": 410}}
			}
			return 200, map[string]any{"nextSyncToken": "sync-3", "items": []map[string]any{
				{"id": "e9", "status": "confirmed", "summary": "After resync", "start": map[string]any{"dateTime": laStart}, "end": map[string]any{"dateTime": laEnd}},
			}}
		}
		return 500, nil
	}

	if err := h.SyncCalendarAccount(ctx, "acc"); err != nil {
		t.Fatalf("full sync: %v", err)
	}
	rangeStart, rangeEnd := now.AddDate(0, 0, -1).Format(time.RFC3339), now.AddDate(0, 0, 10).Format(time.RFC3339)
	code, got := getEvents(t, h, "default", rangeStart, rangeEnd)
	if code != 200 || len(got.Events) != 2 {
		t.Fatalf("after full sync: %d %+v", code, got)
	}
	// All-day first, then the timed event; timed event stored as 16:00Z.
	ad, timed := got.Events[0], got.Events[1]
	if !ad.AllDay || ad.StartDate != day || ad.EndDate != dayAfter || ad.Title != "Offsite" {
		t.Fatalf("all-day = %+v", ad)
	}
	wantStart := timedStart.Add(16 * time.Hour).Format(time.RFC3339)
	if timed.Start != wantStart || timed.Title != "Standup" || timed.CalendarColor != "#9fe1e7" ||
		timed.MeetingURL != "https://meet.google.com/abc" || timed.SelfResponse != "tentative" || timed.Description != "Agenda & notes" {
		t.Fatalf("timed = %+v, want start %s", timed, wantStart)
	}

	phase = "incremental"
	if err := h.SyncCalendarAccount(ctx, "acc"); err != nil {
		t.Fatalf("incremental sync: %v", err)
	}
	_, got = getEvents(t, h, "default", rangeStart, rangeEnd)
	names := map[string]bool{}
	for _, e := range got.Events {
		names[e.Title] = true
	}
	if len(got.Events) != 2 || !names["Standup"] || !names["New"] || names["Offsite"] {
		t.Fatalf("after incremental (cancel e2, add e3) = %+v", got)
	}
	var token string
	_ = db.Read().QueryRow(`SELECT sync_token FROM calendars WHERE provider_calendar_id = 'user@example.com'`).Scan(&token)
	if token != "sync-2" {
		t.Fatalf("token = %q", token)
	}

	phase = "gone"
	if err := h.SyncCalendarAccount(ctx, "acc"); err != nil {
		t.Fatalf("410 resync: %v", err)
	}
	_, got = getEvents(t, h, "default", rangeStart, rangeEnd)
	if len(got.Events) != 1 || got.Events[0].Title != "After resync" {
		t.Fatalf("after 410 resync = %+v", got)
	}
	_ = db.Read().QueryRow(`SELECT sync_token FROM calendars WHERE provider_calendar_id = 'user@example.com'`).Scan(&token)
	if token != "sync-3" {
		t.Fatalf("token after resync = %q", token)
	}
	// The unselected Holidays calendar was never fetched (the fake 500s on it).
	states, _ := db.ListCalendarAccountStates(ctx, "default")
	if states["acc"].NeedsReconnect || states["acc"].LastError != "" {
		t.Fatalf("state = %+v", states["acc"])
	}
}

func TestGoogleCalendarMissingScopeFlagsReconnect(t *testing.T) {
	ctx := context.Background()
	fake := &fakeGoogleCalendar{listCode: http.StatusForbidden}
	h, db := newCalendarHandler(t, fake)
	if err := h.SyncCalendarAccount(ctx, "acc"); !isGoogleCalendarScopeError(err) {
		t.Fatalf("err = %v, want scope error", err)
	}
	rec := httptest.NewRecorder()
	h.handleListCalendars(rec, calendarReq("default", http.MethodGet, "/api/calendar/calendars", nil, ""))
	var resp struct {
		Accounts []struct {
			AccountID      string `json:"account_id"`
			NeedsReconnect bool   `json:"needs_reconnect"`
			Calendars      []any  `json:"calendars"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Accounts) != 1 || !resp.Accounts[0].NeedsReconnect || resp.Accounts[0].Calendars == nil {
		t.Fatalf("response = %s (%v)", rec.Body, err)
	}
	// Recovery: once the scope is granted the flag clears.
	fake.listCode = 0
	fake.events = func(url.Values) (int, map[string]any) { return 200, map[string]any{"nextSyncToken": "t"} }
	if err := h.SyncCalendarAccount(ctx, "acc"); err != nil {
		t.Fatal(err)
	}
	if st, _ := db.ListCalendarAccountStates(ctx, "default"); st["acc"].NeedsReconnect {
		t.Fatal("reconnect flag not cleared after successful sync")
	}
}

func TestCalendarAPIOwnershipAndValidation(t *testing.T) {
	ctx := context.Background()
	fake := &fakeGoogleCalendar{events: func(url.Values) (int, map[string]any) { return 200, map[string]any{"nextSyncToken": "t"} }}
	h, db := newCalendarHandler(t, fake)
	if err := h.SyncCalendarAccount(ctx, "acc"); err != nil {
		t.Fatal(err)
	}
	cals, _ := db.ListCalendarsForUser(ctx, "default")
	if len(cals) != 2 || !cals[0].Selected {
		t.Fatalf("calendars = %+v", cals)
	}
	id := cals[0].ID
	idStr := strconv.FormatInt(id, 10)

	rec := httptest.NewRecorder()
	h.handleSetCalendarSelected(rec, calendarReq("attacker", http.MethodPost, "/x", url.Values{"selected": {"0"}}, idStr))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign toggle = %d", rec.Code)
	}
	if after, _ := db.ListCalendarsForUser(ctx, "default"); !after[0].Selected {
		t.Fatal("foreign toggle changed the calendar")
	}
	rec = httptest.NewRecorder()
	h.handleListCalendars(rec, calendarReq("attacker", http.MethodGet, "/x", nil, ""))
	if strings.Contains(rec.Body.String(), "user@example.com") {
		t.Fatalf("foreign list leaked: %s", rec.Body)
	}
	rec = httptest.NewRecorder()
	h.handleSyncCalendars(rec, calendarReq("attacker", http.MethodPost, "/x", url.Values{}, ""))
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "account_id") {
		t.Fatalf("foreign sync = %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h.handleSetCalendarSelected(rec, calendarReq("default", http.MethodPost, "/x", url.Values{"selected": {"maybe"}}, idStr))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad selected = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.handleSetCalendarSelected(rec, calendarReq("default", http.MethodPost, "/x", url.Values{"selected": {"0"}}, idStr))
	if rec.Code != 200 {
		t.Fatalf("owner toggle = %d", rec.Code)
	}
	if code, _ := getEvents(t, h, "default", "2026-01-01T00:00:00Z", "2026-05-02T00:00:00Z"); code != http.StatusBadRequest {
		t.Fatalf("121-day range = %d, want 400", code)
	}
	if code, _ := getEvents(t, h, "default", "2026-01-01T00:00:00Z", "2026-04-30T00:00:00Z"); code != http.StatusOK {
		t.Fatalf("119-day range = %d, want 200", code)
	}
	if code, _ := getEvents(t, h, "default", "nope", "2026-04-30T00:00:00Z"); code != http.StatusBadRequest {
		t.Fatalf("bad start = %d", code)
	}
}
