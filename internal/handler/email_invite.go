package handler

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/mail/ical"
	"github.com/cristianadrielbraun/gofer/internal/mail/message"
	"github.com/cristianadrielbraun/gofer/internal/storage"
	"github.com/cristianadrielbraun/gofer/internal/views"
)

// handleEmailInvite renders the meeting-invitation card for one message, or
// 204 when the message carries none. The invitation is parsed here, at display
// time, from the stored raw message: nothing is persisted, and the card is a
// read-only view (RSVP is a separate user-initiated POST to the calendar API,
// never triggered by the email). The reading pane fetches it after the body
// iframe has loaded, so the raw message is normally already on disk.
func (h *Handler) handleEmailInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	msgID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || msgID <= 0 {
		http.NotFound(w, r)
		return
	}
	userID := h.userID(ctx)
	info, err := h.db.GetMessageStorageInfoForUser(ctx, msgID, userID)
	if err != nil || info == nil {
		http.NotFound(w, r)
		return
	}

	var raw []byte
	if info.RawPath != "" {
		raw, _ = os.ReadFile(info.RawPath)
	}
	if len(raw) == 0 {
		fetch, ferr := h.db.GetMessageFetchInfoForUser(ctx, msgID, userID)
		if ferr == nil && fetch != nil {
			raw, _ = h.fetchBodyRemote(ctx, msgID, fetch)
		}
	}
	ics := message.ExtractCalendar(bytes.NewReader(raw))
	if ics == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	loc := time.UTC
	if tz := h.db.GetUISettings(ctx, userID)["timezone"]; tz != "" && tz != "local" {
		if l, lerr := time.LoadLocation(tz); lerr == nil {
			loc = l
		}
	}
	inv, err := ical.Parse(ics, loc)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	card, ok := h.inviteCard(r, strconv.FormatInt(msgID, 10), info.AccountID, userID, inv, loc)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	views.EmailInviteCard(card).Render(ctx, w)
}

func (h *Handler) inviteCard(r *http.Request, emailID, accountID, userID string, inv *ical.Invite, loc *time.Location) (views.InviteCard, bool) {
	c := views.InviteCard{
		EmailID: emailID, Kind: "request", Title: inv.Summary, Recurring: inv.Recurring, TZNote: inv.TZUnknown,
		Location: inv.Location, MeetingURL: inv.MeetingURL, Organizer: invitePerson(inv.Organizer), AllDay: inv.AllDay,
	}
	if c.Title == "" {
		c.Title = "(No title)"
	}
	if ical.IsHTTPURL(inv.Location) {
		c.LocationURL = strings.TrimSpace(inv.Location)
	}
	switch {
	case inv.Method == "CANCEL" || inv.Status == "CANCELLED":
		c.Kind = "cancel"
	case inv.Method == "REPLY":
		c.Kind = "reply"
		if len(inv.Attendees) == 0 {
			return c, false
		}
		a := inv.Attendees[0]
		who := a.Name
		if who == "" {
			who = a.Email
		}
		verb := map[string]string{"ACCEPTED": "accepted", "DECLINED": "declined", "TENTATIVE": "tentatively accepted"}[a.PartStat]
		if who == "" || verb == "" {
			return c, false
		}
		c.ReplyLine = who + " " + verb
	case inv.Method == "" || inv.Method == "REQUEST" || inv.Method == "PUBLISH":
	default: // COUNTER, ADD, REFRESH, ...: nothing useful to show
		return c, false
	}
	if !inv.Start.IsZero() {
		c.When, c.StartISO, c.EndISO, c.CalendarDate, c.StartDate, c.EndDate = formatInviteWhen(inv, loc)
	}
	if c.Kind == "request" {
		ev, err := h.db.FindCalendarEventByICalUID(r.Context(), userID, accountID, inv.UID)
		switch {
		case err == nil:
			c.EventID, c.Response = ev.ID, ev.SelfResponse
		case !errors.Is(err, storage.ErrCalendarNotFound):
			return c, false
		}
	}
	return c, true
}

func invitePerson(p ical.Person) string {
	switch {
	case p.Name != "" && p.Email != "" && p.Name != p.Email:
		return p.Name + " (" + p.Email + ")"
	case p.Email != "":
		return p.Email
	}
	return p.Name
}

// formatInviteWhen returns the fallback text plus the machine fields that
// invite-card.js uses to re-render the time in the browser's zone when the
// user's timezone setting is "local".
func formatInviteWhen(inv *ical.Invite, loc *time.Location) (text, startISO, endISO, calDate, startDate, endDate string) {
	const dayFmt = "Mon, Jan 2, 2006"
	if inv.AllDay {
		last := inv.End.AddDate(0, 0, -1) // DTEND is exclusive
		text = inv.Start.Format(dayFmt)
		if last.After(inv.Start) {
			text = inv.Start.Format("Mon, Jan 2") + " – " + last.Format(dayFmt)
		}
		return text, "", "", inv.StartDate, inv.StartDate, inv.EndDate
	}
	s, e := inv.Start.In(loc), inv.End.In(loc)
	zone, _ := s.Zone()
	switch {
	case !inv.End.After(inv.Start):
		text = s.Format(dayFmt+", 3:04 PM") + " " + zone
	case s.Format("2006-01-02") == e.Format("2006-01-02"):
		text = s.Format(dayFmt+", 3:04 PM") + " – " + e.Format("3:04 PM") + " " + zone
	default:
		text = s.Format(dayFmt+", 3:04 PM") + " – " + e.Format(dayFmt+", 3:04 PM") + " " + zone
	}
	return text, inv.Start.UTC().Format(time.RFC3339), inv.End.UTC().Format(time.RFC3339), s.Format("2006-01-02"), "", ""
}
