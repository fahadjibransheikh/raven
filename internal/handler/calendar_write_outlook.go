package handler

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/mailauth"
	"github.com/cristianadrielbraun/gofer/internal/models"
)

// Outlook (Microsoft Graph) side of the calendar write API. The shared handlers
// in calendar_write.go validate the form, then hand over here for Outlook
// calendars. Endpoints and semantics match the Google path.
//
// Graph docs this follows (read 2026-10-04):
//   create  https://learn.microsoft.com/en-us/graph/api/user-post-events          POST /me/calendars/{id}/events
//   update  https://learn.microsoft.com/en-us/graph/api/event-update              PATCH /me/events/{id}
//   delete  https://learn.microsoft.com/en-us/graph/api/event-delete              DELETE /me/events/{id}
//   rsvp    https://learn.microsoft.com/en-us/graph/api/event-accept              POST /me/events/{id}/accept|tentativelyAccept|decline
//   repeat  https://learn.microsoft.com/en-us/graph/api/resources/patternedrecurrence
//   online  https://learn.microsoft.com/en-us/graph/outlook-calendar-online-meetings
//
// Differences from Google worth knowing:
//   - Invitations: Graph always emails attendees on create/update/delete/RSVP
//     ("can't be configured"), so send_updates is accepted but has no effect.
//   - Recurrence: only the five UI presets (none/daily/weekly/monthly/yearly) are
//     accepted; a raw RRULE is rejected with 400 rather than half-translated.
//   - Time zones: timed one-off events are sent as UTC instants. Recurring events
//     are sent as wall-clock time in the user's IANA zone so the series follows
//     DST; Graph documents only a subset of IANA names, and answers 400 (mapped
//     to "rejected") for one it does not know.
//   - add_meet: becomes a Teams online meeting only when the calendar lists
//     teamsForBusiness; personal Outlook.com calendars do not, so the event is
//     created without one and the response carries a "warning".

const graphLocalLayout = "2006-01-02T15:04:05"

func outlookEventPath(id string, suffix ...string) string {
	return "/me/events/" + url.PathEscape(id) + strings.Join(suffix, "")
}

func (h *Handler) outlookWriteToken(ctx context.Context, cal models.Calendar) (string, *calendarError) {
	if h.mailCredentials() == nil {
		return "", &calendarError{http.StatusInternalServerError, "internal", "Calendar is not available."}
	}
	token, err := h.mailCredentials().GetMicrosoftGraphCalendarTokenForAccount(ctx, cal.AccountID)
	if err == nil {
		return token, nil
	}
	if errors.Is(err, mailauth.ErrMicrosoftCalendarConsentRequired) {
		if e := h.db.SetCalendarAccountState(ctx, cal.AccountID, true, "reconnect Outlook to grant calendar access"); e != nil {
			log.Printf("calendar write: record reconnect state: %v", e)
		}
		return "", &calendarError{http.StatusForbidden, "needs_reconnect", "Reconnect this Outlook account to grant calendar access."}
	}
	log.Printf("calendar write token account=%s: %v", cal.AccountID, err)
	return "", &calendarError{http.StatusBadGateway, "auth", "Could not get a Microsoft access token. Try reconnecting the account."}
}

func (h *Handler) mapCalendarOutlookError(err error, accountID string) *calendarError {
	var apiErr outlookAPIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusForbidden:
			return &calendarError{http.StatusForbidden, "read_only", "Microsoft says you do not have permission to change this calendar or event."}
		case http.StatusNotFound, http.StatusGone:
			return &calendarError{http.StatusNotFound, "not_found", "The event no longer exists in Outlook. Sync and try again."}
		case http.StatusBadRequest:
			log.Printf("calendar write rejected by graph: %v", err)
			return &calendarError{http.StatusBadRequest, "rejected", "Outlook rejected the event."}
		case http.StatusTooManyRequests:
			return &calendarError{http.StatusTooManyRequests, "rate_limited", "Outlook is rate limiting requests. Try again shortly."}
		}
	}
	log.Printf("calendar write graph error account=%s: %v", accountID, err)
	return &calendarError{http.StatusBadGateway, "upstream", "Outlook request failed."}
}

// ---------- body builders ----------

// graphTimes renders start/end. All-day events are midnight-to-midnight in UTC;
// recurring timed events use wall-clock time in the event zone (see above).
func (t calendarTimes) graphTimes(recurring bool) (start, end map[string]any) {
	mk := func(v time.Time) map[string]any {
		switch {
		case t.allDay:
			return map[string]any{"dateTime": v.Format(calendarDateLayout) + "T00:00:00", "timeZone": "UTC"}
		case recurring:
			loc, _ := time.LoadLocation(t.tz)
			return map[string]any{"dateTime": v.In(loc).Format(graphLocalLayout), "timeZone": t.tz}
		}
		return map[string]any{"dateTime": v.UTC().Format(graphLocalLayout), "timeZone": "UTC"}
	}
	return mk(t.start), mk(t.end)
}

// graphRecurrence maps the preset (as parseCalendarRecurrence returned it) to
// a patternedRecurrence with no end, starting on the event's first date.
func graphRecurrence(rrule []string, t calendarTimes) (map[string]any, *calendarError) {
	preset := ""
	for name, r := range calendarRecurrences {
		if len(rrule) == 1 && rrule[0] == r {
			preset = name
		}
	}
	if preset == "" {
		return nil, badCalendarRequest("recurrence must be none, daily, weekly, monthly or yearly for Outlook calendars")
	}
	first, tz := t.start, "UTC"
	if !t.allDay {
		loc, _ := time.LoadLocation(t.tz)
		first, tz = t.start.In(loc), t.tz
	}
	pattern := map[string]any{"interval": 1}
	switch preset {
	case "daily":
		pattern["type"] = "daily"
	case "weekly":
		pattern["type"], pattern["daysOfWeek"], pattern["firstDayOfWeek"] = "weekly", []string{strings.ToLower(first.Weekday().String())}, "sunday"
	case "monthly":
		pattern["type"], pattern["dayOfMonth"] = "absoluteMonthly", first.Day()
	case "yearly":
		pattern["type"], pattern["dayOfMonth"], pattern["month"] = "absoluteYearly", first.Day(), int(first.Month())
	}
	return map[string]any{"pattern": pattern, "range": map[string]any{
		"type": "noEnd", "startDate": first.Format(calendarDateLayout), "recurrenceTimeZone": tz,
	}}, nil
}

func graphReminder(form url.Values, body map[string]any) {
	switch v := strings.TrimSpace(form.Get("reminder_minutes")); v {
	case "", "default":
	case "none":
		body["isReminderOn"] = false
	default:
		n, _ := strconv.Atoi(v) // validated by parseCalendarReminder
		body["isReminderOn"], body["reminderMinutesBeforeStart"] = true, n
	}
}

func graphAttendee(email string) map[string]any {
	return map[string]any{"emailAddress": map[string]any{"address": email}, "type": "required"}
}

// onlineMeetingFields returns the body fields that turn the event into an online
// meeting, or a user-facing warning when the calendar cannot host one.
func (h *Handler) onlineMeetingFields(ctx context.Context, token string, cal models.Calendar) (map[string]any, string) {
	const unsupported = "This calendar does not support online meetings (personal Outlook.com calendars do not), so none was added."
	var c graphCalendar
	q := url.Values{"$select": {"allowedOnlineMeetingProviders,defaultOnlineMeetingProvider"}}
	if err := outlookCalendarDo(ctx, token, http.MethodGet, outlookCalendarURL("/me/calendars/"+url.PathEscape(cal.ProviderCalendarID), q), nil, &c); err != nil {
		log.Printf("calendar write: read online meeting providers calendar=%d: %v", cal.ID, err)
		return nil, unsupported
	}
	for _, p := range c.AllowedProviders {
		if p == "teamsForBusiness" {
			return map[string]any{"isOnlineMeeting": true, "onlineMeetingProvider": "teamsForBusiness"}, ""
		}
	}
	return nil, unsupported
}

// ---------- local store ----------

// storeWrittenOutlookEvent mirrors storeWrittenEvent: a single event is upserted
// under the key delta sync uses; a series master (no instance rows exist for it)
// triggers a per-calendar sync instead.
func (h *Handler) storeWrittenOutlookEvent(ctx context.Context, token string, cal models.Calendar, written graphEvent, replacing string) bool {
	// The create/update response does not reliably carry type, so a series master
	// is also recognised by its recurrence. It has no instance rows of its own:
	// expand it instead of caching it as a one-off event.
	if written.ID != "" && (written.Type == "seriesMaster" || (written.Type == "" && len(written.Recurrence) > 0 && string(written.Recurrence) != "null")) {
		if h.refreshOutlookSeries(ctx, token, cal, written.ID) {
			return true
		}
		return h.syncCalendarAfterWrite(ctx, token, cal)
	}
	if written.ID == "" {
		return h.syncCalendarAfterWrite(ctx, token, cal)
	}
	if written.IsCancelled {
		return h.deleteLocalEvents(ctx, cal, written.ID)
	}
	ev, err := outlookEventToModel(written, h.accountEmail(ctx, cal.AccountID))
	var deletes []string
	if replacing != "" && replacing != written.ID {
		deletes = []string{replacing}
	}
	if err == nil {
		err = h.db.ApplyCalendarEventPage(ctx, cal.ID, cal.AccountID, []models.CalendarEvent{ev}, deletes)
	}
	if err != nil {
		log.Printf("calendar write: local upsert calendar=%d event=%s: %v", cal.ID, written.ID, err)
		return false
	}
	return true
}

// ---------- handlers (called after validation) ----------

func (h *Handler) createOutlookEvent(w http.ResponseWriter, r *http.Request, cal models.Calendar, form url.Values, times calendarTimes, guests, recurrence []string) {
	ctx := r.Context()
	var rec map[string]any
	if len(recurrence) > 0 {
		var cerr *calendarError
		if rec, cerr = graphRecurrence(recurrence, times); cerr != nil {
			writeCalendarError(w, cerr)
			return
		}
	}
	token, cerr := h.outlookWriteToken(ctx, cal)
	if cerr != nil {
		writeCalendarError(w, cerr)
		return
	}
	start, end := times.graphTimes(rec != nil)
	body := map[string]any{
		"subject": strings.TrimSpace(form.Get("title")), "location": map[string]any{"displayName": form.Get("location")},
		"body":  map[string]any{"contentType": "text", "content": form.Get("description")},
		"start": start, "end": end, "isAllDay": times.allDay,
	}
	if len(guests) > 0 {
		att := make([]map[string]any, 0, len(guests))
		for _, g := range guests {
			att = append(att, graphAttendee(g))
		}
		body["attendees"] = att
	}
	if rec != nil {
		body["recurrence"] = rec
	}
	graphReminder(form, body)
	warning, wantMeeting := "", false
	if form.Get("add_meet") == "1" {
		var fields map[string]any
		if fields, warning = h.onlineMeetingFields(ctx, token, cal); fields != nil {
			wantMeeting = true
			for k, v := range fields {
				body[k] = v
			}
		}
	}
	var written graphEvent
	if err := outlookCalendarDo(ctx, token, http.MethodPost, outlookCalendarURL("/me/calendars/"+url.PathEscape(cal.ProviderCalendarID)+"/events", nil), body, &written); err != nil {
		writeCalendarError(w, h.mapCalendarOutlookError(err, cal.AccountID))
		return
	}
	if wantMeeting && (written.OnlineMeeting == nil || written.OnlineMeeting.JoinURL == "") {
		warning = "Outlook did not create an online meeting for this event."
	}
	synced := h.storeWrittenOutlookEvent(ctx, token, cal, written, "")
	h.writeOutlookResult(w, map[string]any{"ok": true, "provider_event_id": written.ID, "html_link": written.WebLink, "synced": synced}, warning)
}

func (h *Handler) writeOutlookResult(w http.ResponseWriter, out map[string]any, warning string) {
	if warning != "" {
		out["warning"] = warning
	}
	writeCalendarJSON(w, http.StatusOK, out)
}

func (h *Handler) patchOutlookEvent(w http.ResponseWriter, r *http.Request, ev models.CalendarEventView, cal models.Calendar, form url.Values,
	targetID string, series bool, times calendarTimes, guests, recurrence []string) {
	ctx := r.Context()
	token, cerr := h.outlookWriteToken(ctx, cal)
	if cerr != nil {
		writeCalendarError(w, cerr)
		return
	}
	body := map[string]any{}
	if form.Has("title") {
		body["subject"] = strings.TrimSpace(form.Get("title"))
	}
	if form.Has("location") {
		body["location"] = map[string]any{"displayName": form.Get("location")}
	}
	// Cached descriptions are flattened text; do not rewrite an unchanged one.
	if form.Has("description") && form.Get("description") != ev.Description {
		body["body"] = map[string]any{"contentType": "text", "content": form.Get("description")}
	}
	graphReminder(form, body)
	warning := ""
	if form.Get("add_meet") == "1" && ev.MeetingURL == "" {
		var fields map[string]any
		if fields, warning = h.onlineMeetingFields(ctx, token, cal); fields != nil {
			for k, v := range fields {
				body[k] = v
			}
		}
	}
	if len(recurrence) > 0 {
		t := times
		if !t.set { // keep the event where it is; recurrence just needs its first date
			t = calendarTimes{set: true, allDay: ev.AllDay, tz: h.calendarDefaultTZ(ctx, cal), start: ev.StartAt, end: ev.EndAt}
		}
		rec, cerr := graphRecurrence(recurrence, t)
		if cerr != nil {
			writeCalendarError(w, cerr)
			return
		}
		body["recurrence"] = rec
	}
	if form.Has("attendees") {
		var cur struct {
			Attendees []map[string]any `json:"attendees"`
		}
		q := url.Values{"$select": {"attendees"}}
		if err := outlookCalendarDo(ctx, token, http.MethodGet, outlookCalendarURL(outlookEventPath(targetID), q), nil, &cur); err != nil {
			writeCalendarError(w, h.mapCalendarOutlookError(err, cal.AccountID))
			return
		}
		body["attendees"] = mergeGraphGuests(cur.Attendees, guests)
	}
	if times.set {
		if series {
			var master graphEvent
			q := url.Values{"$select": {"start,end,isAllDay"}}
			if err := outlookCalendarDo(ctx, token, http.MethodGet, outlookCalendarURL(outlookEventPath(targetID), q), nil, &master); err != nil {
				writeCalendarError(w, h.mapCalendarOutlookError(err, cal.AccountID))
				return
			}
			if s, e, changed := shiftGraphSeriesTimes(master, ev.CalendarEvent, times); changed {
				body["start"], body["end"], body["isAllDay"] = s, e, times.allDay
			}
		} else {
			body["start"], body["end"] = times.graphTimes(len(recurrence) > 0)
			body["isAllDay"] = times.allDay
		}
	}
	if len(body) == 0 {
		writeCalendarJSON(w, http.StatusOK, map[string]any{"ok": true, "provider_event_id": targetID, "synced": true})
		return
	}
	var written graphEvent
	if err := outlookCalendarDo(ctx, token, http.MethodPatch, outlookCalendarURL(outlookEventPath(targetID), nil), body, &written); err != nil {
		writeCalendarError(w, h.mapCalendarOutlookError(err, cal.AccountID))
		return
	}
	var synced bool
	if series {
		synced = h.refreshOutlookSeries(ctx, token, cal, targetID) || h.syncCalendarAfterWrite(ctx, token, cal)
	} else {
		synced = h.storeWrittenOutlookEvent(ctx, token, cal, written, ev.ProviderEventID)
	}
	h.writeOutlookResult(w, map[string]any{"ok": true, "provider_event_id": written.ID, "html_link": written.WebLink, "synced": synced}, warning)
}

// mergeGraphGuests builds the attendees array for a patch (Graph replaces the
// whole list): resources are kept, listed guests keep their existing entry (and
// response), new ones are bare required attendees, unlisted ones are dropped.
func mergeGraphGuests(existing []map[string]any, emails []string) []map[string]any {
	addr := func(a map[string]any) string {
		ea, _ := a["emailAddress"].(map[string]any)
		s, _ := ea["address"].(string)
		return strings.ToLower(s)
	}
	out := []map[string]any{}
	byEmail := map[string]map[string]any{}
	for _, a := range existing {
		if t, _ := a["type"].(string); t == "resource" {
			out = append(out, a)
		} else {
			byEmail[addr(a)] = a
		}
	}
	for _, e := range emails {
		if a, ok := byEmail[strings.ToLower(e)]; ok {
			out = append(out, a)
		} else {
			out = append(out, graphAttendee(e))
		}
	}
	return out
}

// shiftGraphSeriesTimes is shiftSeriesTimes for Graph: "this occurrence moved
// from A to B" becomes the same wall-clock shift of the series master.
func shiftGraphSeriesTimes(master graphEvent, old models.CalendarEvent, nt calendarTimes) (start, end map[string]any, changed bool) {
	loc, _ := time.LoadLocation(nt.tz)
	wall := func(t time.Time) time.Time {
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
	ms, err := parseGraphDateTime(master.Start)
	if err != nil {
		ms = newS.Add(-delta)
	} else if master.IsAllDay {
		ms = nearestMidnight(ms)
	} else {
		ms = wall(ms)
	}
	mstart := ms.Add(delta)
	mend := mstart.Add(dur)
	if nt.allDay {
		return map[string]any{"dateTime": mstart.Format(calendarDateLayout) + "T00:00:00", "timeZone": "UTC"},
			map[string]any{"dateTime": mend.Format(calendarDateLayout) + "T00:00:00", "timeZone": "UTC"}, true
	}
	return map[string]any{"dateTime": mstart.Format(graphLocalLayout), "timeZone": nt.tz},
		map[string]any{"dateTime": mend.Format(graphLocalLayout), "timeZone": nt.tz}, true
}

func (h *Handler) deleteOutlookEvent(w http.ResponseWriter, r *http.Request, ev models.CalendarEventView, cal models.Calendar, targetID string, series bool) {
	ctx := r.Context()
	token, cerr := h.outlookWriteToken(ctx, cal)
	if cerr != nil {
		writeCalendarError(w, cerr)
		return
	}
	err := outlookCalendarDo(ctx, token, http.MethodDelete, outlookCalendarURL(outlookEventPath(targetID), nil), nil, nil)
	var apiErr outlookAPIError
	if err != nil && !(errors.As(err, &apiErr) && (apiErr.Status == http.StatusGone || apiErr.Status == http.StatusNotFound)) {
		if errors.As(err, &apiErr) {
			log.Printf("calendar delete: graph DELETE %s failed with status %d: %v", targetID, apiErr.Status, err)
		}
		writeCalendarError(w, h.mapCalendarOutlookError(err, cal.AccountID)) // already deleted upstream counts as success
		return
	}
	// Deleting a series master removes every occurrence upstream, and delta need
	// not report them, so drop the whole series locally. targetID is the master
	// for series scope; it is also a master when the row we were given is one
	// (cached before it was recognised), and harmless for a plain occurrence.
	synced := h.db.DeleteCalendarSeries(ctx, cal.ID, targetID) == nil
	if !series {
		synced = h.deleteLocalEvents(ctx, cal, ev.ProviderEventID) && synced
	}
	writeCalendarJSON(w, http.StatusOK, map[string]any{"ok": true, "synced": synced})
}

var graphRSVPActions = map[string]string{"accepted": "/accept", "declined": "/decline", "tentative": "/tentativelyAccept"}

func (h *Handler) rsvpOutlookEvent(w http.ResponseWriter, r *http.Request, ev models.CalendarEventView, cal models.Calendar, targetID string, series bool, response string) {
	ctx := r.Context()
	// The organizer cannot respond to their own meeting; Graph has no "self"
	// flag, so compare with the account address.
	if len(ev.Attendees) == 0 || strings.EqualFold(ev.OrganizerEmail, h.accountEmail(ctx, cal.AccountID)) {
		writeCalendarError(w, badCalendarRequest("you are not a guest of this event"))
		return
	}
	token, cerr := h.outlookWriteToken(ctx, cal)
	if cerr != nil {
		writeCalendarError(w, cerr)
		return
	}
	if err := outlookCalendarDo(ctx, token, http.MethodPost, outlookCalendarURL(outlookEventPath(targetID, graphRSVPActions[response]), nil),
		map[string]any{"sendResponse": true}, nil); err != nil {
		writeCalendarError(w, h.mapCalendarOutlookError(err, cal.AccountID))
		return
	}
	var synced bool
	if series {
		synced = h.refreshOutlookSeries(ctx, token, cal, targetID) || h.syncCalendarAfterWrite(ctx, token, cal)
	} else {
		// 202 carries no body: read the event back. Declining can remove it from the calendar.
		var written graphEvent
		err := outlookCalendarDo(ctx, token, http.MethodGet, outlookCalendarURL(outlookEventPath(targetID), nil), nil, &written)
		var apiErr outlookAPIError
		switch {
		case errors.As(err, &apiErr) && (apiErr.Status == http.StatusNotFound || apiErr.Status == http.StatusGone):
			synced = h.deleteLocalEvents(ctx, cal, ev.ProviderEventID)
		case err != nil:
			log.Printf("calendar rsvp: read back event=%s: %v", targetID, err)
		default:
			synced = h.storeWrittenOutlookEvent(ctx, token, cal, written, ev.ProviderEventID)
		}
	}
	writeCalendarJSON(w, http.StatusOK, map[string]any{"ok": true, "response": response, "synced": synced})
}
