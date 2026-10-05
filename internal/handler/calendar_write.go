package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cristianadrielbraun/gofer/internal/models"
	"github.com/cristianadrielbraun/gofer/internal/providers"
	"github.com/cristianadrielbraun/gofer/internal/storage"
)

// Calendar write API (C2): create, edit, delete and RSVP against Google
// Calendar (Outlook calendars are handed to calendar_write_outlook.go after
// the shared validation below). {id} is the local calendar_events.id and, for create, calendar_id
// is the local calendars.id; both are resolved through the request user's
// accounts before any Google call. Only owner/writer calendars are written.
//
// Google docs this follows (read 2026-10-04):
//   events.insert  https://developers.google.com/workspace/calendar/api/v3/reference/events/insert
//   events.patch   https://developers.google.com/workspace/calendar/api/v3/reference/events/patch
//   events.delete  https://developers.google.com/workspace/calendar/api/v3/reference/events/delete
//   recurring      https://developers.google.com/workspace/calendar/api/guides/recurringevents
//   conferences    https://developers.google.com/workspace/calendar/api/guides/create-events

const (
	calendarMaxTitle       = 1024
	calendarMaxLocation    = 1024
	calendarMaxDescription = 8192
	calendarMaxAttendees   = 100
	calendarMaxReminderMin = 40320 // Google's limit: 4 weeks
	calendarMaxRRULE       = 500
)

var (
	calendarRRULEPattern = regexp.MustCompile(`^RRULE:[A-Za-z0-9=;,+\-]+$`)
	calendarRecurrences  = map[string]string{
		"daily": "RRULE:FREQ=DAILY", "weekly": "RRULE:FREQ=WEEKLY",
		"monthly": "RRULE:FREQ=MONTHLY", "yearly": "RRULE:FREQ=YEARLY",
	}
)

// calendarError carries an HTTP status plus a machine code the UI can branch on.
type calendarError struct {
	Status  int
	Code    string
	Message string
}

func (e *calendarError) Error() string { return e.Message }

func badCalendarRequest(format string, args ...any) *calendarError {
	return &calendarError{http.StatusBadRequest, "invalid", fmt.Sprintf(format, args...)}
}

func writeCalendarError(w http.ResponseWriter, e *calendarError) {
	writeCalendarJSON(w, e.Status, map[string]string{"error": e.Code, "message": e.Message})
}

// ---------- Google transport ----------

func googleCalendarDo(ctx context.Context, accessToken, method, path string, q url.Values, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	target := googleCalendarAPIBaseURL + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return newGoogleAPIError(resp, b)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func calendarEventPath(providerCalendarID, providerEventID string) string {
	p := "/calendars/" + url.PathEscape(providerCalendarID) + "/events"
	if providerEventID != "" {
		p += "/" + url.PathEscape(providerEventID)
	}
	return p
}

// googleRawEvent is the slice of an event we read back before patching. The
// attendee entries stay untyped so fields we do not model survive a round trip.
type googleRawEvent struct {
	Start     googleEventTime  `json:"start"`
	End       googleEventTime  `json:"end"`
	Attendees []map[string]any `json:"attendees"`
}

// ---------- request authorisation + error mapping ----------

// calendarWriteToken checks the calendar is writable and returns a Google token.
func (h *Handler) calendarWriteToken(ctx context.Context, cal models.Calendar) (string, *calendarError) {
	if cal.AccessRole != "owner" && cal.AccessRole != "writer" {
		return "", &calendarError{http.StatusForbidden, "read_only", "This calendar is read-only."}
	}
	if h.mailCredentials() == nil {
		return "", &calendarError{http.StatusInternalServerError, "internal", "Calendar is not available."}
	}
	if cal.Provider == providers.ProviderOutlook {
		return h.outlookWriteToken(ctx, cal)
	}
	token, err := h.mailCredentials().GetOAuthTokenForAccount(ctx, cal.AccountID)
	if err != nil {
		log.Printf("calendar write token account=%s: %v", cal.AccountID, err)
		return "", &calendarError{http.StatusBadGateway, "auth", "Could not get a Google access token. Try reconnecting the account."}
	}
	return token, nil
}

func (h *Handler) mapCalendarGoogleError(ctx context.Context, cal models.Calendar, err error) *calendarError {
	var apiErr googleAPIError
	switch {
	case isGoogleCalendarScopeError(err):
		if e := h.db.SetCalendarAccountState(ctx, cal.AccountID, true, "reconnect Google to grant calendar access"); e != nil {
			log.Printf("calendar write: record reconnect state: %v", e)
		}
		return &calendarError{http.StatusForbidden, "needs_reconnect", "Reconnect this Google account to grant calendar access."}
	case errors.As(err, &apiErr):
		switch apiErr.Status {
		case http.StatusForbidden:
			return &calendarError{http.StatusForbidden, "read_only", "Google says you do not have permission to change this calendar or event."}
		case http.StatusNotFound, http.StatusGone:
			return &calendarError{http.StatusNotFound, "not_found", "The event no longer exists in Google Calendar. Sync and try again."}
		case http.StatusBadRequest:
			log.Printf("calendar write rejected by google: %v", err)
			return &calendarError{http.StatusBadRequest, "rejected", "Google Calendar rejected the event."}
		case http.StatusTooManyRequests:
			return &calendarError{http.StatusTooManyRequests, "rate_limited", "Google Calendar is rate limiting requests. Try again shortly."}
		}
	}
	log.Printf("calendar write google error account=%s: %v", cal.AccountID, err)
	return &calendarError{http.StatusBadGateway, "upstream", "Google Calendar request failed."}
}

// ---------- local store after a write ----------

// storeWrittenEvent makes the local cache reflect a successful write without
// touching the stored syncToken. A single (non-master) event returned by
// Google is upserted: the row key is (calendar_id, provider_event_id), the same
// key incremental sync uses, so the later sync that re-delivers this change
// overwrites it instead of duplicating it. A recurring master carries no
// instance rows (the cache holds singleEvents=true expansions), so it, and any
// series-wide change, triggers an incremental sync of just that calendar.
func (h *Handler) storeWrittenEvent(ctx context.Context, token string, cal models.Calendar, written googleCalendarEvent) bool {
	if len(written.Recurrence) > 0 || written.ID == "" {
		return h.syncCalendarAfterWrite(ctx, token, cal)
	}
	if written.Status == "cancelled" {
		return h.deleteLocalEvents(ctx, cal, written.ID)
	}
	ev, err := googleEventToModel(written)
	if err == nil {
		err = h.db.ApplyCalendarEventPage(ctx, cal.ID, cal.AccountID, []models.CalendarEvent{ev}, nil)
	}
	if err != nil {
		log.Printf("calendar write: local upsert calendar=%d event=%s: %v", cal.ID, written.ID, err)
		return false
	}
	return true
}

func (h *Handler) deleteLocalEvents(ctx context.Context, cal models.Calendar, providerEventID string) bool {
	if err := h.db.ApplyCalendarEventPage(ctx, cal.ID, cal.AccountID, nil, []string{providerEventID}); err != nil {
		log.Printf("calendar write: local delete calendar=%d event=%s: %v", cal.ID, providerEventID, err)
		return false
	}
	return true
}

// syncCalendarAfterWrite runs the normal (incremental) sync for one calendar,
// under the same per-account guard as full account syncs. Unselected calendars
// are not synced at all, so they stay untouched until the user selects them.
func (h *Handler) syncCalendarAfterWrite(ctx context.Context, token string, cal models.Calendar) bool {
	if !cal.Selected {
		return true
	}
	if !h.beginCalendarSync(cal.AccountID) {
		return false // a sync is already running; the next one picks the change up
	}
	defer h.endCalendarSync(cal.AccountID)
	fresh, err := h.db.GetCalendarForUser(ctx, h.userID(ctx), cal.ID)
	if err == nil {
		if cal.Provider == providers.ProviderOutlook {
			err = h.syncOutlookCalendarEvents(ctx, token, h.accountEmail(ctx, cal.AccountID), fresh)
		} else {
			err = h.syncGoogleCalendarEvents(ctx, token, fresh)
		}
	}
	if err != nil {
		log.Printf("calendar write: sync calendar=%d: %v", cal.ID, err)
		return false
	}
	return true
}

// ---------- field parsing ----------

type calendarTimes struct {
	set        bool
	allDay     bool
	tz         string
	start, end time.Time // all-day: dates at 00:00 UTC, end exclusive
}

func (h *Handler) calendarDefaultTZ(ctx context.Context, cal models.Calendar) string {
	if tz := strings.TrimSpace(h.db.GetUISettings(ctx, h.userID(ctx))["timezone"]); tz != "" && tz != "local" {
		if _, err := time.LoadLocation(tz); err == nil {
			return tz
		}
	}
	if _, err := time.LoadLocation(cal.TimeZone); err == nil && cal.TimeZone != "" {
		return cal.TimeZone
	}
	return "UTC"
}

func parseCalendarTimes(form url.Values, defaultTZ string) (calendarTimes, *calendarError) {
	var t calendarTimes
	if !form.Has("all_day") && !form.Has("start") && !form.Has("start_date") {
		return t, nil
	}
	t.set = true
	t.allDay = form.Get("all_day") == "1"
	t.tz = strings.TrimSpace(form.Get("time_zone"))
	if t.tz == "" {
		t.tz = defaultTZ
	}
	if t.tz == "Local" {
		return t, badCalendarRequest("time_zone must be an IANA name")
	}
	if _, err := time.LoadLocation(t.tz); err != nil {
		return t, badCalendarRequest("time_zone must be an IANA name")
	}
	if t.allDay {
		var err error
		if t.start, err = time.Parse(calendarDateLayout, form.Get("start_date")); err != nil {
			return t, badCalendarRequest("start_date must be YYYY-MM-DD")
		}
		t.end = t.start.AddDate(0, 0, 1)
		if v := form.Get("end_date"); v != "" {
			if t.end, err = time.Parse(calendarDateLayout, v); err != nil {
				return t, badCalendarRequest("end_date must be YYYY-MM-DD")
			}
		}
		if !t.end.After(t.start) {
			return t, badCalendarRequest("end_date must be after start_date (end is exclusive)")
		}
		return t, nil
	}
	var err1, err2 error
	t.start, err1 = time.Parse(time.RFC3339, form.Get("start"))
	t.end, err2 = time.Parse(time.RFC3339, form.Get("end"))
	if err1 != nil || err2 != nil {
		return t, badCalendarRequest("start and end must be RFC3339")
	}
	if !t.end.After(t.start) {
		return t, badCalendarRequest("end must be after start")
	}
	return t, nil
}

// googleTimes renders the start/end objects. For patches the unused member is
// an explicit null: patch is a JSON merge, so switching between timed and
// all-day would otherwise keep the old dateTime/date next to the new one.
func (t calendarTimes) googleTimes(patch bool) (start, end map[string]any) {
	mk := func(v time.Time) map[string]any {
		m := map[string]any{}
		if t.allDay {
			m["date"] = v.Format(calendarDateLayout)
			if patch {
				m["dateTime"], m["timeZone"] = nil, nil
			}
		} else {
			m["dateTime"], m["timeZone"] = v.Format(time.RFC3339), t.tz
			if patch {
				m["date"] = nil
			}
		}
		return m
	}
	return mk(t.start), mk(t.end)
}

func parseCalendarGuests(raw string) ([]string, *calendarError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	list, err := mail.ParseAddressList(raw)
	if err != nil {
		return nil, badCalendarRequest("attendees must be a comma-separated list of valid email addresses")
	}
	seen := map[string]bool{}
	var out []string
	for _, a := range list {
		key := strings.ToLower(a.Address)
		if !seen[key] {
			seen[key] = true
			out = append(out, a.Address)
		}
	}
	if len(out) > calendarMaxAttendees {
		return nil, badCalendarRequest("at most %d attendees are allowed", calendarMaxAttendees)
	}
	return out, nil
}

// parseCalendarRecurrence returns nil for "none"/empty. Only a single RRULE
// line is accepted (no EXDATE/RDATE/DTSTART lines, no newlines).
func parseCalendarRecurrence(v string) ([]string, *calendarError) {
	v = strings.TrimSpace(v)
	if v == "" || v == "none" {
		return nil, nil
	}
	if r, ok := calendarRecurrences[v]; ok {
		return []string{r}, nil
	}
	if len(v) > calendarMaxRRULE || !calendarRRULEPattern.MatchString(v) || !strings.Contains(v, "FREQ=") {
		return nil, badCalendarRequest("recurrence must be none, daily, weekly, monthly, yearly or a single RRULE line")
	}
	return []string{v}, nil
}

func parseCalendarReminder(v string) (map[string]any, *calendarError) {
	switch v = strings.TrimSpace(v); v {
	case "", "default":
		return map[string]any{"useDefault": true}, nil
	case "none":
		return map[string]any{"useDefault": false, "overrides": []any{}}, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > calendarMaxReminderMin {
		return nil, badCalendarRequest("reminder_minutes must be default, none or 0-%d", calendarMaxReminderMin)
	}
	return map[string]any{"useDefault": false, "overrides": []any{map[string]any{"method": "popup", "minutes": n}}}, nil
}

func checkCalendarText(form url.Values) *calendarError {
	for _, f := range []struct {
		name string
		max  int
	}{{"title", calendarMaxTitle}, {"location", calendarMaxLocation}, {"description", calendarMaxDescription}} {
		if utf8.RuneCountInString(form.Get(f.name)) > f.max {
			return badCalendarRequest("%s must be at most %d characters", f.name, f.max)
		}
	}
	return nil
}

// resolveSendUpdates applies the "never email unless there are guests" rule.
func resolveSendUpdates(requested string, hasGuests bool) (string, *calendarError) {
	switch requested {
	case "", "all", "externalOnly", "none":
	default:
		return "", badCalendarRequest("send_updates must be all, externalOnly or none")
	}
	if !hasGuests {
		return "none", nil
	}
	if requested == "" {
		return "all", nil
	}
	return requested, nil
}

func storedGuestCount(ev models.CalendarEvent) int {
	n := 0
	for _, a := range ev.Attendees {
		if !a.Self && !a.Resource {
			n++
		}
	}
	return n
}

func newMeetRequest() map[string]any {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return map[string]any{"createRequest": map[string]any{
		"requestId":             hex.EncodeToString(b),
		"conferenceSolutionKey": map[string]any{"type": "hangoutsMeet"},
	}}
}

// mergeGuests builds the attendees array for a patch: organizer/self/resource
// entries are always kept, listed guests keep their existing entry (and so
// their response status), new ones are bare {email}, unlisted ones are dropped.
func mergeGuests(existing []map[string]any, emails []string) []map[string]any {
	out := []map[string]any{}
	byEmail := map[string]map[string]any{}
	for _, a := range existing {
		email, _ := a["email"].(string)
		keep := false
		for _, k := range []string{"organizer", "self", "resource"} {
			if v, _ := a[k].(bool); v {
				keep = true
			}
		}
		if keep {
			out = append(out, a)
		} else {
			byEmail[strings.ToLower(email)] = a
		}
	}
	for _, e := range emails {
		if a, ok := byEmail[strings.ToLower(e)]; ok {
			out = append(out, a)
		} else {
			out = append(out, map[string]any{"email": e})
		}
	}
	return out
}

// ---------- handlers ----------

func (h *Handler) handleCreateCalendarEvent(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeCalendarError(w, badCalendarRequest("invalid form data"))
		return
	}
	ctx := r.Context()
	form := r.PostForm
	calID, err := strconv.ParseInt(form.Get("calendar_id"), 10, 64)
	if err != nil {
		writeCalendarError(w, badCalendarRequest("calendar_id is required"))
		return
	}
	cal, err := h.db.GetCalendarForUser(ctx, h.userID(ctx), calID)
	if errors.Is(err, storage.ErrCalendarNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("create calendar event: load calendar %d: %v", calID, err)
		http.Error(w, "failed to load calendar", http.StatusInternalServerError)
		return
	}
	// Validate everything before any Google call; a read-only calendar is
	// still reported as 403 first, since the body could never be accepted.
	if cal.AccessRole != "owner" && cal.AccessRole != "writer" {
		writeCalendarError(w, &calendarError{http.StatusForbidden, "read_only", "This calendar is read-only."})
		return
	}
	if e := checkCalendarText(form); e != nil {
		writeCalendarError(w, e)
		return
	}
	times, cerr := parseCalendarTimes(form, h.calendarDefaultTZ(ctx, cal))
	if cerr == nil && !times.set {
		cerr = badCalendarRequest("start and end (or all_day=1 with start_date) are required")
	}
	var guests, recurrence []string
	var reminders map[string]any
	if cerr == nil {
		guests, cerr = parseCalendarGuests(form.Get("attendees"))
	}
	if cerr == nil {
		recurrence, cerr = parseCalendarRecurrence(form.Get("recurrence"))
	}
	if cerr == nil {
		reminders, cerr = parseCalendarReminder(form.Get("reminder_minutes"))
	}
	var sendUpdates string
	if cerr == nil {
		sendUpdates, cerr = resolveSendUpdates(form.Get("send_updates"), len(guests) > 0)
	}
	if cerr != nil {
		writeCalendarError(w, cerr)
		return
	}
	if cal.Provider == providers.ProviderOutlook {
		h.createOutlookEvent(w, r, cal, form, times, guests, recurrence)
		return
	}
	token, cerr := h.calendarWriteToken(ctx, cal)
	if cerr != nil {
		writeCalendarError(w, cerr)
		return
	}

	start, end := times.googleTimes(false)
	body := map[string]any{
		"summary": strings.TrimSpace(form.Get("title")), "location": form.Get("location"),
		"description": form.Get("description"), "start": start, "end": end,
	}
	if len(guests) > 0 {
		att := make([]map[string]any, 0, len(guests))
		for _, g := range guests {
			att = append(att, map[string]any{"email": g})
		}
		body["attendees"] = att
	}
	if len(recurrence) > 0 {
		body["recurrence"] = recurrence
	}
	if form.Has("reminder_minutes") && form.Get("reminder_minutes") != "" {
		body["reminders"] = reminders
	}
	q := url.Values{"sendUpdates": {sendUpdates}}
	if form.Get("add_meet") == "1" {
		body["conferenceData"] = newMeetRequest()
		q.Set("conferenceDataVersion", "1")
	}
	var written googleCalendarEvent
	if err := googleCalendarDo(ctx, token, http.MethodPost, calendarEventPath(cal.ProviderCalendarID, ""), q, body, &written); err != nil {
		writeCalendarError(w, h.mapCalendarGoogleError(ctx, cal, err))
		return
	}
	synced := h.storeWrittenEvent(ctx, token, cal, written)
	writeCalendarJSON(w, http.StatusOK, map[string]any{
		"ok": true, "provider_event_id": written.ID, "html_link": written.HTMLLink, "synced": synced,
	})
}

// calendarWriteTarget loads the event addressed by {id} and its calendar.
// ok=false means a response was already written.
func (h *Handler) calendarWriteTarget(w http.ResponseWriter, r *http.Request) (ev models.CalendarEventView, cal models.Calendar, ok bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ev, cal, err = h.db.GetCalendarEventForUser(r.Context(), h.userID(r.Context()), id)
	if errors.Is(err, storage.ErrCalendarNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("calendar event %d: %v", id, err)
		http.Error(w, "failed to load event", http.StatusInternalServerError)
		return
	}
	return ev, cal, true
}

// parseCalendarScope returns the provider event id to address: the instance
// itself for "this" (and for non-recurring events), the series master for
// "series" on an instance.
func parseCalendarScope(v string, ev models.CalendarEvent) (targetID string, series bool, err *calendarError) {
	switch v {
	case "", "this":
		return ev.ProviderEventID, false, nil
	case "series":
		if ev.RecurringEventID != "" {
			return ev.RecurringEventID, true, nil
		}
		return ev.ProviderEventID, false, nil
	}
	return "", false, badCalendarRequest("scope must be this or series")
}

func (h *Handler) handlePatchCalendarEvent(w http.ResponseWriter, r *http.Request) {
	ev, cal, ok := h.calendarWriteTarget(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeCalendarError(w, badCalendarRequest("invalid form data"))
		return
	}
	ctx := r.Context()
	form := r.PostForm
	if cal.AccessRole != "owner" && cal.AccessRole != "writer" {
		writeCalendarError(w, &calendarError{http.StatusForbidden, "read_only", "This calendar is read-only."})
		return
	}
	targetID, series, cerr := parseCalendarScope(form.Get("scope"), ev.CalendarEvent)
	if cerr == nil {
		cerr = checkCalendarText(form)
	}
	var times calendarTimes
	if cerr == nil {
		defTZ := ev.EventTimeZone
		if _, err := time.LoadLocation(defTZ); err != nil || defTZ == "Local" { // Outlook may store a Windows zone name
			defTZ = ""
		}
		if defTZ == "" {
			defTZ = h.calendarDefaultTZ(ctx, cal)
		}
		times, cerr = parseCalendarTimes(form, defTZ)
	}
	var guests, recurrence []string
	if cerr == nil && form.Has("attendees") {
		guests, cerr = parseCalendarGuests(form.Get("attendees"))
	}
	if cerr == nil && form.Has("recurrence") {
		recurrence, cerr = parseCalendarRecurrence(form.Get("recurrence"))
		if cerr == nil && len(recurrence) > 0 && ev.RecurringEventID != "" {
			cerr = badCalendarRequest("changing the repeat rule of an existing recurring event is not supported")
		}
	}
	var reminders map[string]any
	if cerr == nil && form.Has("reminder_minutes") {
		reminders, cerr = parseCalendarReminder(form.Get("reminder_minutes"))
	}
	var sendUpdates string
	if cerr == nil {
		sendUpdates, cerr = resolveSendUpdates(form.Get("send_updates"), len(guests) > 0 || storedGuestCount(ev.CalendarEvent) > 0)
	}
	if cerr != nil {
		writeCalendarError(w, cerr)
		return
	}
	if cal.Provider == providers.ProviderOutlook {
		h.patchOutlookEvent(w, r, ev, cal, form, targetID, series, times, guests, recurrence)
		return
	}
	token, cerr := h.calendarWriteToken(ctx, cal)
	if cerr != nil {
		writeCalendarError(w, cerr)
		return
	}

	body := map[string]any{}
	if form.Has("title") {
		body["summary"] = strings.TrimSpace(form.Get("title"))
	}
	if form.Has("location") {
		body["location"] = form.Get("location")
	}
	// The cache stores descriptions flattened to plain text; writing back an
	// unchanged value would strip the original's formatting.
	if form.Has("description") && form.Get("description") != ev.Description {
		body["description"] = form.Get("description")
	}
	if reminders != nil {
		body["reminders"] = reminders
	}
	if len(recurrence) > 0 {
		body["recurrence"] = recurrence
	}
	q := url.Values{"sendUpdates": {sendUpdates}}
	if form.Get("add_meet") == "1" && ev.MeetingURL == "" {
		body["conferenceData"] = newMeetRequest()
		q.Set("conferenceDataVersion", "1")
	}

	var current googleRawEvent
	if form.Has("attendees") || (series && times.set) {
		if err := googleCalendarDo(ctx, token, http.MethodGet, calendarEventPath(cal.ProviderCalendarID, targetID), nil, nil, &current); err != nil {
			writeCalendarError(w, h.mapCalendarGoogleError(ctx, cal, err))
			return
		}
	}
	if form.Has("attendees") {
		body["attendees"] = mergeGuests(current.Attendees, guests)
	}
	if times.set {
		if series {
			if s, e, changed := shiftSeriesTimes(current, ev.CalendarEvent, times); changed {
				body["start"], body["end"] = s, e
			}
		} else {
			body["start"], body["end"] = times.googleTimes(true)
		}
	}
	if len(body) == 0 {
		writeCalendarJSON(w, http.StatusOK, map[string]any{"ok": true, "provider_event_id": targetID, "synced": true})
		return
	}
	var written googleCalendarEvent
	if err := googleCalendarDo(ctx, token, http.MethodPatch, calendarEventPath(cal.ProviderCalendarID, targetID), q, body, &written); err != nil {
		writeCalendarError(w, h.mapCalendarGoogleError(ctx, cal, err))
		return
	}
	var synced bool
	if series {
		synced = h.syncCalendarAfterWrite(ctx, token, cal)
	} else {
		synced = h.storeWrittenEvent(ctx, token, cal, written)
	}
	writeCalendarJSON(w, http.StatusOK, map[string]any{
		"ok": true, "provider_event_id": written.ID, "html_link": written.HTMLLink, "synced": synced,
	})
}

// shiftSeriesTimes turns "this occurrence moved from A to B" into the same
// wall-clock shift of the series master, so editing all events never re-bases
// the series onto the occurrence's date. Reports changed=false when the
// occurrence keeps its start and length (only other fields were edited).
func shiftSeriesTimes(master googleRawEvent, old models.CalendarEvent, nt calendarTimes) (start, end map[string]any, changed bool) {
	loc, _ := time.LoadLocation(nt.tz)
	wall := func(t time.Time) time.Time { // the clock face in loc as a floating time
		t = t.In(loc)
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
	}
	oldS, oldE := old.StartAt, old.EndAt
	if !old.AllDay {
		oldS, oldE = wall(oldS), wall(oldE)
	}
	newS, newE := nt.start, nt.end
	if !nt.allDay {
		newS, newE = wall(newS), wall(newE)
	}
	delta, dur := newS.Sub(oldS), newE.Sub(newS)
	if nt.allDay == old.AllDay && delta == 0 && dur == oldE.Sub(oldS) {
		return nil, nil, false
	}
	base := time.Time{}
	masterTZ := master.Start.TimeZone
	if master.Start.Date != "" {
		base, _ = time.Parse(calendarDateLayout, master.Start.Date)
	} else if t, err := time.Parse(time.RFC3339, master.Start.DateTime); err == nil {
		base = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
	}
	if base.IsZero() {
		base = newS.Add(-delta)
	}
	ms := base.Add(delta)
	me := ms.Add(dur)
	if masterTZ == "" {
		masterTZ = nt.tz
	}
	if nt.allDay {
		return map[string]any{"date": ms.Format(calendarDateLayout), "dateTime": nil, "timeZone": nil},
			map[string]any{"date": me.Format(calendarDateLayout), "dateTime": nil, "timeZone": nil}, true
	}
	const wallLayout = "2006-01-02T15:04:05"
	return map[string]any{"dateTime": ms.Format(wallLayout), "timeZone": masterTZ, "date": nil},
		map[string]any{"dateTime": me.Format(wallLayout), "timeZone": masterTZ, "date": nil}, true
}

func (h *Handler) handleDeleteCalendarEvent(w http.ResponseWriter, r *http.Request) {
	ev, cal, ok := h.calendarWriteTarget(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if cal.AccessRole != "owner" && cal.AccessRole != "writer" {
		writeCalendarError(w, &calendarError{http.StatusForbidden, "read_only", "This calendar is read-only."})
		return
	}
	targetID, series, cerr := parseCalendarScope(r.URL.Query().Get("scope"), ev.CalendarEvent)
	var sendUpdates string
	if cerr == nil {
		sendUpdates, cerr = resolveSendUpdates(r.URL.Query().Get("send_updates"), storedGuestCount(ev.CalendarEvent) > 0)
	}
	if cerr != nil {
		writeCalendarError(w, cerr)
		return
	}
	if cal.Provider == providers.ProviderOutlook {
		h.deleteOutlookEvent(w, r, ev, cal, targetID, series)
		return
	}
	token, cerr := h.calendarWriteToken(ctx, cal)
	if cerr != nil {
		writeCalendarError(w, cerr)
		return
	}
	err := googleCalendarDo(ctx, token, http.MethodDelete, calendarEventPath(cal.ProviderCalendarID, targetID), url.Values{"sendUpdates": {sendUpdates}}, nil, nil)
	var apiErr googleAPIError
	if err != nil && !(errors.As(err, &apiErr) && (apiErr.Status == http.StatusGone || apiErr.Status == http.StatusNotFound)) {
		writeCalendarError(w, h.mapCalendarGoogleError(ctx, cal, err)) // already deleted upstream counts as success
		return
	}
	var synced bool
	if series {
		synced = h.syncCalendarAfterWrite(ctx, token, cal)
	} else {
		synced = h.deleteLocalEvents(ctx, cal, ev.ProviderEventID)
	}
	writeCalendarJSON(w, http.StatusOK, map[string]any{"ok": true, "synced": synced})
}

func (h *Handler) handleRSVPCalendarEvent(w http.ResponseWriter, r *http.Request) {
	ev, cal, ok := h.calendarWriteTarget(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeCalendarError(w, badCalendarRequest("invalid form data"))
		return
	}
	ctx := r.Context()
	form := r.PostForm
	response := form.Get("response")
	if response != "accepted" && response != "declined" && response != "tentative" {
		writeCalendarError(w, badCalendarRequest("response must be accepted, declined or tentative"))
		return
	}
	targetID, series, cerr := parseCalendarScope(form.Get("scope"), ev.CalendarEvent)
	var sendUpdates string
	if cerr == nil {
		if form.Get("send_updates") == "" {
			form.Set("send_updates", "all") // the organizer is the guest who needs to hear about it
		}
		sendUpdates, cerr = resolveSendUpdates(form.Get("send_updates"), true)
	}
	if cerr == nil {
		self := false
		for _, a := range ev.Attendees {
			self = self || a.Self
		}
		if !self && cal.Provider != providers.ProviderOutlook { // Outlook has no self flag; rsvpOutlookEvent checks
			cerr = badCalendarRequest("you are not a guest of this event")
		}
	}
	if cerr != nil {
		writeCalendarError(w, cerr)
		return
	}
	if cal.Provider == providers.ProviderOutlook {
		h.rsvpOutlookEvent(w, r, ev, cal, targetID, series, response)
		return
	}
	token, cerr := h.calendarWriteToken(ctx, cal)
	if cerr != nil {
		writeCalendarError(w, cerr)
		return
	}
	var current googleRawEvent
	if err := googleCalendarDo(ctx, token, http.MethodGet, calendarEventPath(cal.ProviderCalendarID, targetID), nil, nil, &current); err != nil {
		writeCalendarError(w, h.mapCalendarGoogleError(ctx, cal, err))
		return
	}
	found := false
	for _, a := range current.Attendees {
		if self, _ := a["self"].(bool); self {
			a["responseStatus"] = response
			found = true
		}
	}
	if !found {
		writeCalendarError(w, badCalendarRequest("you are not a guest of this event"))
		return
	}
	var written googleCalendarEvent
	if err := googleCalendarDo(ctx, token, http.MethodPatch, calendarEventPath(cal.ProviderCalendarID, targetID),
		url.Values{"sendUpdates": {sendUpdates}}, map[string]any{"attendees": current.Attendees}, &written); err != nil {
		writeCalendarError(w, h.mapCalendarGoogleError(ctx, cal, err))
		return
	}
	var synced bool
	if series {
		synced = h.syncCalendarAfterWrite(ctx, token, cal)
	} else {
		synced = h.storeWrittenEvent(ctx, token, cal, written)
	}
	writeCalendarJSON(w, http.StatusOK, map[string]any{"ok": true, "response": response, "synced": synced})
}
