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

	"github.com/cristianadrielbraun/gofer/internal/mail/ical"
	"github.com/cristianadrielbraun/gofer/internal/models"
	"github.com/google/uuid"
)

// iCloud (CalDAV) side of the calendar write API. The shared handlers in
// calendar_write.go validate the form, then hand over here for iCloud
// calendars. Same endpoints and semantics as Google and Outlook.
//
// Every change is read-modify-write of one .ics resource: GET (for the current
// body and ETag), edit only the lines we mean to change (so VALARMs, X-
// properties and VTIMEZONEs survive), PUT with If-Match (RFC 4791 5.3.2).
// A 412 means the resource changed since the GET; the local copy is refreshed
// and the user is told to retry. Creates use If-None-Match: *.
//
//   create            PUT <calendar>/<uuid>.ics
//   edit occurrence   RECURRENCE-ID override VEVENT in the same resource
//   edit series       the master VEVENT (times shifted by the wall-clock delta)
//   delete occurrence EXDATE on the master (an override for it is dropped)
//   delete series     DELETE with If-Match
//   RSVP              PARTSTAT of our ATTENDEE line only; the server delivers
//                     the iTIP REPLY (RFC 6638 3.2.2.3). SEQUENCE is not touched
//                     because attendees may change only their own participation.
//
// Differences from the other providers:
//   - Invitations/replies are sent by the server whenever attendees exist
//     (SCHEDULE-AGENT=SERVER is the default), so send_updates has no effect.
//   - add_meet is unsupported: the event is created and the response carries a "warning".
//   - Timed one-off events are written in UTC. Recurring (and already zoned)
//     events use TZID with the IANA name and no VTIMEZONE; whether iCloud
//     accepts that is unverified (needs a live test).

const icloudNoMeetWarning = "iCloud Calendar cannot add an online meeting, so the event was saved without one."

func (h *Handler) mapCalendarICloudError(ctx context.Context, err error, cal models.Calendar) *calendarError {
	var he icloudHTTPError
	if errors.As(err, &he) {
		switch he.Status {
		case http.StatusUnauthorized:
			if e := h.db.SetCalendarAccountState(ctx, cal.AccountID, true, icloudReconnectMsg); e != nil {
				log.Printf("calendar write: record reconnect state: %v", e)
			}
			return &calendarError{http.StatusForbidden, "needs_reconnect", "iCloud rejected the app-specific password. Update it in Settings > Accounts > Edit."}
		case http.StatusPreconditionFailed:
			return &calendarError{http.StatusConflict, "conflict", "This event changed elsewhere. It has been refreshed; try your change again."}
		case http.StatusForbidden:
			return &calendarError{http.StatusForbidden, "read_only", "iCloud says you do not have permission to change this calendar or event."}
		case http.StatusNotFound, http.StatusGone:
			return &calendarError{http.StatusNotFound, "not_found", "The event no longer exists in iCloud. Sync and try again."}
		case http.StatusTooManyRequests:
			return &calendarError{http.StatusTooManyRequests, "rate_limited", "iCloud is rate limiting requests. Try again shortly."}
		case http.StatusBadRequest, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
			log.Printf("calendar write rejected by icloud: %v", err)
			return &calendarError{http.StatusBadRequest, "rejected", "iCloud Calendar rejected the event."}
		}
	}
	log.Printf("calendar write icloud error account=%s: %v", cal.AccountID, err)
	return &calendarError{http.StatusBadGateway, "upstream", "iCloud request failed."}
}

func (h *Handler) icloudWriteClient(ctx context.Context, cal models.Calendar) (*icloudClient, *calendarError) {
	c, err := h.icloudClientFor(ctx, cal.AccountID)
	if err != nil {
		log.Printf("calendar write icloud client account=%s: %v", cal.AccountID, err)
		return nil, &calendarError{http.StatusBadGateway, "auth", "Could not use the stored iCloud password. Update it in Settings > Accounts > Edit."}
	}
	return c, nil
}

// ---------- resource access ----------

type icloudDoc struct {
	URL  string
	Key  string
	ETag string
	Res  *ical.Resource
}

func (c *icloudClient) getResource(ctx context.Context, calURL, key string) (*icloudDoc, error) {
	target := icloudResourceURL(calURL, key)
	reply, err := c.do(ctx, http.MethodGet, target, nil, nil)
	if err != nil {
		return nil, err
	}
	res, err := ical.ParseResource(reply.Body, time.UTC)
	if err != nil {
		return nil, err
	}
	return &icloudDoc{URL: target, Key: key, ETag: reply.Header.Get("ETag"), Res: res}, nil
}

func (c *icloudClient) putResource(ctx context.Context, target, etag string, body []byte) error {
	hdr := map[string]string{"Content-Type": "text/calendar; charset=utf-8"}
	if etag != "" {
		hdr["If-Match"] = etag
	} else {
		hdr["If-None-Match"] = "*"
	}
	_, err := c.do(ctx, http.MethodPut, target, hdr, body)
	return err
}

func (c *icloudClient) deleteResource(ctx context.Context, target, etag string) error {
	var hdr map[string]string
	if etag != "" {
		hdr = map[string]string{"If-Match": etag}
	}
	_, err := c.do(ctx, http.MethodDelete, target, hdr, nil)
	return err
}

// refreshICloudResource re-reads one resource into the local cache. keepSelf
// are addresses already known to be the user's (flagged on the stored event).
func (h *Handler) refreshICloudResource(ctx context.Context, c *icloudClient, cal models.Calendar, key string, keepSelf []string) bool {
	self := h.icloudSelfAddresses(ctx, cal.AccountID, keepSelf)
	from, to := time.Now().Add(-calendarInitialPast), time.Now().Add(calendarInitialFuture)
	if cal.WindowStart != nil && cal.WindowEnd != nil {
		from, to = *cal.WindowStart, *cal.WindowEnd
	}
	var events []models.CalendarEvent
	reply, err := c.do(ctx, http.MethodGet, icloudResourceURL(cal.ProviderCalendarID, key), nil, nil)
	switch {
	case isICloudStatus(err, http.StatusNotFound, http.StatusGone):
	case err != nil:
		log.Printf("calendar write: refresh %s: %v", key, err)
		return false
	default:
		if events, err = icloudResourceEvents(key, reply.Body, self, h.icloudFloatingZone(ctx, cal), from, to); err != nil {
			log.Printf("calendar write: refresh %s: %v", key, err)
			return false
		}
	}
	if err := h.db.ApplyCalendarResources(ctx, cal.ID, cal.AccountID, []string{key}, events); err != nil {
		log.Printf("calendar write: local update calendar=%d resource=%s: %v", cal.ID, key, err)
		return false
	}
	return true
}

func selfEmailsOf(ev models.CalendarEvent) []string {
	var out []string
	for _, a := range ev.Attendees {
		if a.Self {
			out = append(out, a.Email)
		}
	}
	return out
}

// icloudTarget splits a stored event id into its resource key and recurrence id.
func icloudTarget(ev models.CalendarEvent) (key string, recID time.Time, isOccurrence bool) {
	key, rid, ok := strings.Cut(ev.ProviderEventID, "#")
	if !ok {
		return key, time.Time{}, false
	}
	if t, err := time.Parse(icloudRecIDLayout, rid); err == nil {
		return key, t, true
	}
	if t, err := time.Parse("20060102", rid); err == nil {
		return key, t, true
	}
	return key, time.Time{}, true
}

// ---------- body builders ----------

func (h *Handler) icloudOrganizerLines(ctx context.Context, cal models.Calendar) (organizer string) {
	return h.accountEmail(ctx, cal.AccountID)
}

func icloudAttendeeLine(c *ical.Comp, email string, partstat string) {
	c.Add("ATTENDEE;PARTSTAT="+partstat+";ROLE=REQ-PARTICIPANT;RSVP=TRUE;CN="+quoteParam(email), "mailto:"+email)
}

func quoteParam(s string) string {
	return `"` + strings.NewReplacer(`"`, "", "\r", "", "\n", "").Replace(s) + `"`
}

func stampEvent(c *ical.Comp, now time.Time, bumpSequence bool) {
	c.Set("DTSTAMP", ical.UTCStamp(now))
	c.Set("LAST-MODIFIED", ical.UTCStamp(now))
	if bumpSequence {
		n := 0
		if p := c.Get("SEQUENCE"); p != nil {
			n, _ = strconv.Atoi(strings.TrimSpace(p.Value))
		}
		c.Set("SEQUENCE", strconv.Itoa(n+1))
	}
}

func icloudSetTimes(c *ical.Comp, start, end time.Time, allDay bool, tz string) {
	h, v := ical.TimeProp("DTSTART", start, allDay, tz)
	c.Set(h, v)
	h, v = ical.TimeProp("DTEND", end, allDay, tz)
	c.Set(h, v)
	c.Remove("DURATION")
}

func icloudSetAlarm(c *ical.Comp, minutes string) {
	if minutes == "" || minutes == "default" {
		return
	}
	var kept []*ical.Comp
	for _, ch := range c.Children {
		if ch.Name != "VALARM" {
			kept = append(kept, ch)
		}
	}
	c.Children = kept
	n, err := strconv.Atoi(minutes)
	if err != nil { // "none"
		return
	}
	a := &ical.Comp{Name: "VALARM"}
	a.Add("ACTION", "DISPLAY")
	a.Add("DESCRIPTION", "Reminder")
	a.Add("TRIGGER", "-PT"+strconv.Itoa(n)+"M")
	c.Children = append(c.Children, a)
}

func icloudRRule(recurrence []string) string {
	return strings.TrimPrefix(recurrence[0], "RRULE:")
}

// ---------- create ----------

func (h *Handler) createICloudEvent(w http.ResponseWriter, r *http.Request, cal models.Calendar, form url.Values, times calendarTimes, guests, recurrence []string) {
	ctx := r.Context()
	c, cerr := h.icloudWriteClient(ctx, cal)
	if cerr != nil {
		writeCalendarError(w, cerr)
		return
	}
	now := time.Now()
	id := uuid.NewString()
	cc := ical.NewCalendar()
	ev := &ical.Comp{Name: "VEVENT"}
	ev.Add("UID", id)
	ev.Add("DTSTAMP", ical.UTCStamp(now))
	ev.Add("CREATED", ical.UTCStamp(now))
	ev.Add("LAST-MODIFIED", ical.UTCStamp(now))
	ev.Add("SEQUENCE", "0")
	ev.Add("SUMMARY", ical.EscapeText(strings.TrimSpace(form.Get("title"))))
	tz := ""
	if len(recurrence) > 0 {
		tz = times.tz
	}
	icloudSetTimes(ev, times.start, times.end, times.allDay, tz)
	if v := form.Get("location"); v != "" {
		ev.Add("LOCATION", ical.EscapeText(v))
	}
	if v := form.Get("description"); v != "" {
		ev.Add("DESCRIPTION", ical.EscapeText(v))
	}
	if len(recurrence) > 0 {
		ev.Add("RRULE", icloudRRule(recurrence))
	}
	if len(guests) > 0 {
		org := h.icloudOrganizerLines(ctx, cal)
		ev.Add("ORGANIZER;CN="+quoteParam(org), "mailto:"+org)
		ev.Add("ATTENDEE;PARTSTAT=ACCEPTED;ROLE=CHAIR;CN="+quoteParam(org), "mailto:"+org)
		for _, g := range guests {
			if !strings.EqualFold(g, org) {
				icloudAttendeeLine(ev, g, "NEEDS-ACTION")
			}
		}
	}
	icloudSetAlarm(ev, strings.TrimSpace(form.Get("reminder_minutes")))
	cc.Children = append(cc.Children, ev)
	key := icloudResourceKey(icloudResourceURL(cal.ProviderCalendarID, id+".ics"))
	if err := c.putResource(ctx, icloudResourceURL(cal.ProviderCalendarID, key), "", cc.Bytes()); err != nil {
		writeCalendarError(w, h.mapCalendarICloudError(ctx, err, cal))
		return
	}
	synced := h.refreshICloudResource(ctx, c, cal, key, nil)
	out := map[string]any{"ok": true, "provider_event_id": key, "synced": synced}
	if form.Get("add_meet") == "1" {
		out["warning"] = icloudNoMeetWarning
	}
	writeCalendarJSON(w, http.StatusOK, out)
}

// ---------- edit ----------

// icloudOccurrenceComp returns the VEVENT that holds this occurrence: the
// existing override, or a new one cloned from the master (without its
// recurrence parts) and placed at the occurrence's own time.
func icloudOccurrenceComp(doc *icloudDoc, master *ical.Event, stored models.CalendarEvent, recID time.Time) *ical.Comp {
	if ov := doc.Res.Override(recID); ov != nil {
		return ov.Comp
	}
	c := master.Comp.Clone()
	for _, n := range []string{"RRULE", "RDATE", "EXDATE", "RECURRENCE-ID"} {
		c.Remove(n)
	}
	h, v := ical.TimeProp("RECURRENCE-ID", recID, master.AllDay, master.TZID)
	c.Add(h, v)
	icloudSetTimes(c, stored.StartAt, stored.EndAt, master.AllDay, master.TZID)
	doc.Res.Root.Children = append(doc.Res.Root.Children, c)
	return c
}

// shiftICloudMasterTimes turns "this occurrence moved from A to B" into the same
// wall-clock shift of the series master, as shiftSeriesTimes does for Google.
func shiftICloudMasterTimes(master *ical.Event, old models.CalendarEvent, nt calendarTimes) (start, end time.Time, changed bool) {
	loc, _ := time.LoadLocation(nt.tz)
	wall := func(t time.Time, l *time.Location) time.Time {
		t = t.In(l)
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
	}
	oldS, oldE := old.StartAt, old.EndAt
	if !old.AllDay {
		oldS, oldE = wall(oldS, loc), wall(oldE, loc)
	}
	newS, newE := nt.start, nt.end
	if !nt.allDay {
		newS, newE = wall(newS, loc), wall(newE, loc)
	}
	delta, dur := newS.Sub(oldS), newE.Sub(newS)
	if nt.allDay == old.AllDay && delta == 0 && dur == oldE.Sub(oldS) {
		return start, end, false
	}
	base := master.Start
	if !nt.allDay {
		base = wall(master.Start, master.Loc)
	}
	ms := base.Add(delta)
	me := ms.Add(dur)
	if nt.allDay {
		return ms, me, true
	}
	return time.Date(ms.Year(), ms.Month(), ms.Day(), ms.Hour(), ms.Minute(), ms.Second(), 0, master.Loc),
		time.Date(me.Year(), me.Month(), me.Day(), me.Hour(), me.Minute(), me.Second(), 0, master.Loc), true
}

func (h *Handler) patchICloudEvent(w http.ResponseWriter, r *http.Request, ev models.CalendarEventView, cal models.Calendar, form url.Values,
	targetID string, series bool, times calendarTimes, guests, recurrence []string) {
	ctx := r.Context()
	c, cerr := h.icloudWriteClient(ctx, cal)
	if cerr != nil {
		writeCalendarError(w, cerr)
		return
	}
	key, recID, occurrence := icloudTarget(ev.CalendarEvent)
	doc, err := c.getResource(ctx, cal.ProviderCalendarID, key)
	if err != nil {
		writeCalendarError(w, h.mapCalendarICloudError(ctx, err, cal))
		return
	}
	master := doc.Res.Master()
	var target *ical.Comp
	switch {
	case master == nil: // overrides only (invited to single occurrences)
		ov := doc.Res.Override(recID)
		if ov == nil {
			writeCalendarError(w, &calendarError{http.StatusNotFound, "not_found", "The event no longer exists in iCloud. Sync and try again."})
			return
		}
		target = ov.Comp
	case occurrence && !series:
		target = icloudOccurrenceComp(doc, master, ev.CalendarEvent, recID)
	default:
		target = master.Comp
	}
	tzOf := func() string {
		if master != nil && master.TZID != "" {
			return master.TZID
		}
		if len(recurrence) > 0 || (master != nil && master.RRule != "") {
			return times.tz
		}
		return ""
	}
	changed := false
	if form.Has("title") {
		target.Set("SUMMARY", ical.EscapeText(strings.TrimSpace(form.Get("title"))))
		changed = true
	}
	if form.Has("location") {
		setOrDrop(target, "LOCATION", form.Get("location"))
		changed = true
	}
	if form.Has("description") && form.Get("description") != ev.Description {
		setOrDrop(target, "DESCRIPTION", form.Get("description"))
		changed = true
	}
	if form.Has("reminder_minutes") {
		icloudSetAlarm(target, strings.TrimSpace(form.Get("reminder_minutes")))
		changed = changed || strings.TrimSpace(form.Get("reminder_minutes")) != "default"
	}
	if len(recurrence) > 0 {
		target.Set("RRULE", icloudRRule(recurrence))
		changed = true
	}
	if times.set {
		if series && occurrence && master != nil {
			if s, e, ch := shiftICloudMasterTimes(master, ev.CalendarEvent, times); ch {
				icloudSetTimes(master.Comp, s, e, times.allDay, tzOf())
				changed = true
			}
		} else {
			icloudSetTimes(target, times.start, times.end, times.allDay, tzOf())
			changed = true
		}
	}
	if form.Has("attendees") {
		h.icloudMergeGuests(ctx, target, cal, guests, selfEmailsOf(ev.CalendarEvent))
		changed = true
	}
	if !changed {
		writeCalendarJSON(w, http.StatusOK, map[string]any{"ok": true, "provider_event_id": ev.ProviderEventID, "synced": true})
		return
	}
	stampEvent(target, time.Now(), true)
	if err := c.putResource(ctx, doc.URL, doc.ETag, doc.Res.Root.Bytes()); err != nil {
		if isICloudStatus(err, http.StatusPreconditionFailed) {
			h.refreshICloudResource(ctx, c, cal, key, selfEmailsOf(ev.CalendarEvent))
		}
		writeCalendarError(w, h.mapCalendarICloudError(ctx, err, cal))
		return
	}
	synced := h.refreshICloudResource(ctx, c, cal, key, selfEmailsOf(ev.CalendarEvent))
	out := map[string]any{"ok": true, "provider_event_id": ev.ProviderEventID, "synced": synced}
	if form.Get("add_meet") == "1" && ev.MeetingURL == "" {
		out["warning"] = icloudNoMeetWarning
	}
	writeCalendarJSON(w, http.StatusOK, out)
}

func setOrDrop(c *ical.Comp, name, value string) {
	if value == "" {
		c.Remove(name)
		return
	}
	c.Set(name, ical.EscapeText(value))
}

// icloudMergeGuests makes the ATTENDEE set equal to guests: our own entry and
// the organizer's stay, listed guests keep their line (and PARTSTAT), new ones
// start at NEEDS-ACTION, unlisted ones are dropped.
func (h *Handler) icloudMergeGuests(ctx context.Context, c *ical.Comp, cal models.Calendar, guests, selfKnown []string) {
	self := h.icloudSelfAddresses(ctx, cal.AccountID, selfKnown)
	organizer := ""
	if o := c.Get("ORGANIZER"); o != nil {
		organizer = strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(o.Value, "mailto:"), "MAILTO:"))
	}
	existing := c.All("ATTENDEE")
	c.Remove("ATTENDEE")
	byEmail := map[string]ical.Prop{}
	for _, p := range existing {
		addr := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(p.Value, "mailto:"), "MAILTO:"))
		if self[addr] || addr == organizer {
			c.Props = append(c.Props, p)
		} else {
			byEmail[addr] = p
		}
	}
	for _, g := range guests {
		if self[strings.ToLower(g)] {
			continue
		}
		if p, ok := byEmail[strings.ToLower(g)]; ok {
			c.Props = append(c.Props, p)
		} else {
			icloudAttendeeLine(c, g, "NEEDS-ACTION")
		}
	}
	if len(guests) > 0 && c.Get("ORGANIZER") == nil {
		org := h.icloudOrganizerLines(ctx, cal)
		c.Add("ORGANIZER;CN="+quoteParam(org), "mailto:"+org)
		c.Add("ATTENDEE;PARTSTAT=ACCEPTED;ROLE=CHAIR;CN="+quoteParam(org), "mailto:"+org)
	}
}

// ---------- delete ----------

func (h *Handler) deleteICloudEvent(w http.ResponseWriter, r *http.Request, ev models.CalendarEventView, cal models.Calendar, series bool) {
	ctx := r.Context()
	c, cerr := h.icloudWriteClient(ctx, cal)
	if cerr != nil {
		writeCalendarError(w, cerr)
		return
	}
	key, recID, occurrence := icloudTarget(ev.CalendarEvent)
	doc, err := c.getResource(ctx, cal.ProviderCalendarID, key)
	if isICloudStatus(err, http.StatusNotFound, http.StatusGone) { // already gone upstream counts as success
		synced := h.refreshICloudResource(ctx, c, cal, key, nil)
		writeCalendarJSON(w, http.StatusOK, map[string]any{"ok": true, "synced": synced})
		return
	}
	if err != nil {
		writeCalendarError(w, h.mapCalendarICloudError(ctx, err, cal))
		return
	}
	master := doc.Res.Master()
	if !occurrence || series || (master == nil && len(doc.Res.Events) <= 1) {
		err = c.deleteResource(ctx, doc.URL, doc.ETag)
	} else {
		// This occurrence only: EXDATE it (RFC 5545 3.8.5.1) and drop its override.
		if ov := doc.Res.Override(recID); ov != nil {
			kids := doc.Res.Root.Children[:0]
			for _, ch := range doc.Res.Root.Children {
				if ch != ov.Comp {
					kids = append(kids, ch)
				}
			}
			doc.Res.Root.Children = kids
		}
		if master != nil {
			hd, v := ical.TimeProp("EXDATE", recID, master.AllDay, master.TZID)
			master.Comp.Add(hd, v)
			stampEvent(master.Comp, time.Now(), true)
		}
		err = c.putResource(ctx, doc.URL, doc.ETag, doc.Res.Root.Bytes())
	}
	if err != nil {
		if isICloudStatus(err, http.StatusPreconditionFailed) {
			h.refreshICloudResource(ctx, c, cal, key, selfEmailsOf(ev.CalendarEvent))
		}
		if !isICloudStatus(err, http.StatusNotFound, http.StatusGone) {
			writeCalendarError(w, h.mapCalendarICloudError(ctx, err, cal))
			return
		}
	}
	synced := h.refreshICloudResource(ctx, c, cal, key, selfEmailsOf(ev.CalendarEvent))
	writeCalendarJSON(w, http.StatusOK, map[string]any{"ok": true, "synced": synced})
}

// ---------- RSVP ----------

var icloudPartStat = map[string]string{"accepted": "ACCEPTED", "declined": "DECLINED", "tentative": "TENTATIVE"}

func (h *Handler) rsvpICloudEvent(w http.ResponseWriter, r *http.Request, ev models.CalendarEventView, cal models.Calendar, series bool, response string) {
	ctx := r.Context()
	c, cerr := h.icloudWriteClient(ctx, cal)
	if cerr != nil {
		writeCalendarError(w, cerr)
		return
	}
	key, recID, occurrence := icloudTarget(ev.CalendarEvent)
	doc, err := c.getResource(ctx, cal.ProviderCalendarID, key)
	if err != nil {
		writeCalendarError(w, h.mapCalendarICloudError(ctx, err, cal))
		return
	}
	master := doc.Res.Master()
	var target *ical.Comp
	switch {
	case master == nil:
		if ov := doc.Res.Override(recID); ov != nil {
			target = ov.Comp
		}
	case occurrence && !series:
		target = icloudOccurrenceComp(doc, master, ev.CalendarEvent, recID)
	default:
		target = master.Comp
	}
	self := h.icloudSelfAddresses(ctx, cal.AccountID, selfEmailsOf(ev.CalendarEvent))
	found := false
	if target != nil {
		for i, p := range target.Props {
			if p.Name() != "ATTENDEE" {
				continue
			}
			if self[strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(p.Value, "mailto:"), "MAILTO:"))] {
				target.Props[i] = p.WithParam("PARTSTAT", icloudPartStat[response])
				found = true
			}
		}
	}
	if !found {
		writeCalendarError(w, badCalendarRequest("you are not a guest of this event"))
		return
	}
	// Only PARTSTAT changes: RFC 6638 3.2.2.3 makes the server send the REPLY,
	// and attendees may not change SEQUENCE or anything else.
	if err := c.putResource(ctx, doc.URL, doc.ETag, doc.Res.Root.Bytes()); err != nil {
		if isICloudStatus(err, http.StatusPreconditionFailed) {
			h.refreshICloudResource(ctx, c, cal, key, selfEmailsOf(ev.CalendarEvent))
		}
		writeCalendarError(w, h.mapCalendarICloudError(ctx, err, cal))
		return
	}
	synced := h.refreshICloudResource(ctx, c, cal, key, selfEmailsOf(ev.CalendarEvent))
	writeCalendarJSON(w, http.StatusOK, map[string]any{"ok": true, "response": response, "synced": synced})
}
