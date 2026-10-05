package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/mailauth"
	"github.com/cristianadrielbraun/gofer/internal/models"
)

// Outlook (Microsoft Graph) calendar sync. Docs this follows (read 2026-10-04):
//   list calendars   https://learn.microsoft.com/en-us/graph/api/user-list-calendars
//   calendarView     https://learn.microsoft.com/en-us/graph/api/calendar-list-calendarview
//   calendarView delta https://learn.microsoft.com/en-us/graph/api/event-delta
//                      https://learn.microsoft.com/en-us/graph/delta-query-events
//   delta tokens     https://learn.microsoft.com/en-us/graph/delta-query-overview
//
// Delta semantics used here: the first call (startDateTime/endDateTime) walks
// @odata.nextLink pages to an @odata.deltaLink and returns every event in the
// window (expanded occurrences, exceptions and single instances). The deltaLink
// encodes the window, so it is stored verbatim as the calendar's sync_token and
// requested as-is next time. Removals arrive as {"id", "@removed":{reason}};
// they can also name events outside the window, which are harmless no-ops here.
// A stale token answers 410 or a syncStateNotFound code, which forces a full
// resync (the same fallback Google uses for 410).

var errOutlookDeltaExpired = errors.New("outlook calendar delta token expired")

// outlookCalendarSleep waits out a 429 Retry-After; tests replace it.
var outlookCalendarSleep = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

const (
	outlookCalendarMaxAttempts = 3
	outlookCalendarMaxWait     = 60 * time.Second
)

// outlookCalendarDo performs one Graph request against a full URL. Times come
// back in UTC. A 429 is retried (the request was not processed) after the
// server's Retry-After, up to a bounded wait; the final failure is returned as
// an outlookAPIError so callers can map the status.
func outlookCalendarDo(ctx context.Context, accessToken, method, target string, body, out any, extraPrefer ...string) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	prefer := strings.Join(append([]string{`outlook.timezone="UTC"`}, extraPrefer...), ", ")
	for attempt := 1; ; attempt++ {
		var rd io.Reader
		if payload != nil {
			rd = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, target, rd)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Prefer", prefer)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return err
		}
		raw, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests && attempt < outlookCalendarMaxAttempts {
			wait := time.Duration(attempt) * 2 * time.Second
			if at := providerRetryAfter(resp); !at.IsZero() {
				wait = time.Until(at)
			}
			if wait <= outlookCalendarMaxWait {
				if wait < 0 {
					wait = 0
				}
				if err := outlookCalendarSleep(ctx, wait); err != nil {
					return err
				}
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return newOutlookAPIError(resp, raw)
		}
		if readErr != nil {
			return readErr
		}
		if out == nil || len(bytes.TrimSpace(raw)) == 0 {
			return nil
		}
		return json.Unmarshal(raw, out)
	}
}

func outlookCalendarURL(path string, q url.Values) string {
	u := outlookGraphBaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// checkGraphLink refuses to send the bearer token to anything but the Graph
// host the sync is configured for (nextLink/deltaLink come from the response).
func checkGraphLink(link string) error {
	if !strings.HasPrefix(link, outlookGraphBaseURL+"/") {
		return fmt.Errorf("unexpected graph link %q", link)
	}
	return nil
}

func isOutlookCalendarScopeError(err error) bool {
	return errors.Is(err, mailauth.ErrMicrosoftCalendarConsentRequired)
}

func isOutlookDeltaExpired(err error) bool {
	var apiErr outlookAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	body := strings.ToLower(apiErr.Body)
	return apiErr.Status == http.StatusGone || strings.Contains(body, "syncstatenotfound") || strings.Contains(body, "resyncrequired")
}

// ---------- wire types ----------

type graphDateTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

type graphEmail struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type graphEvent struct {
	ID                    string                                `json:"id"`
	ICalUID               string                                `json:"iCalUId"`
	Type                  string                                `json:"type"`
	SeriesMasterID        string                                `json:"seriesMasterId"`
	Subject               string                                `json:"subject"`
	Body                  struct{ ContentType, Content string } `json:"body"`
	Location              struct{ DisplayName string }          `json:"location"`
	Start                 graphDateTime                         `json:"start"`
	End                   graphDateTime                         `json:"end"`
	IsAllDay              bool                                  `json:"isAllDay"`
	IsCancelled           bool                                  `json:"isCancelled"`
	IsOrganizer           bool                                  `json:"isOrganizer"`
	OriginalStartTimeZone string                                `json:"originalStartTimeZone"`
	Organizer             struct {
		EmailAddress graphEmail `json:"emailAddress"`
	} `json:"organizer"`
	Attendees []struct {
		Type         string     `json:"type"` // required, optional, resource
		EmailAddress graphEmail `json:"emailAddress"`
		Status       struct {
			Response string `json:"response"`
		} `json:"status"`
	} `json:"attendees"`
	ResponseStatus struct {
		Response string `json:"response"`
	} `json:"responseStatus"`
	OnlineMeeting *struct {
		JoinURL string `json:"joinUrl"`
	} `json:"onlineMeeting"`
	OnlineMeetingURL string                   `json:"onlineMeetingUrl"`
	WebLink          string                   `json:"webLink"`
	LastModified     string                   `json:"lastModifiedDateTime"`
	Removed          *struct{ Reason string } `json:"@removed"`
}

type graphEventsPage struct {
	Value     []graphEvent `json:"value"`
	NextLink  string       `json:"@odata.nextLink"`
	DeltaLink string       `json:"@odata.deltaLink"`
}

type graphCalendar struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Color             string     `json:"color"`
	HexColor          string     `json:"hexColor"`
	CanEdit           bool       `json:"canEdit"`
	IsDefaultCalendar bool       `json:"isDefaultCalendar"`
	Owner             graphEmail `json:"owner"`
	AllowedProviders  []string   `json:"allowedOnlineMeetingProviders"`
	DefaultProvider   string     `json:"defaultOnlineMeetingProvider"`
}

// Outlook's named calendar colours (approximate swatches; Graph only gives the
// hex when the user picked a custom one).
var graphCalendarColors = map[string]string{
	"lightBlue": "#71afe5", "lightGreen": "#9fdc9f", "lightOrange": "#f7b680", "lightGray": "#b9b9b9",
	"lightYellow": "#f7e07b", "lightTeal": "#7fd4d0", "lightPink": "#f0a3c8", "lightBrown": "#c8a98d",
	"lightRed": "#ee8c8c", "maxColor": "#0078d4",
}

const graphDefaultCalendarColor = "#0078d4"

func (h *Handler) accountEmail(ctx context.Context, accountID string) string {
	var email string
	_ = h.db.Read().QueryRowContext(ctx, `SELECT email_address FROM accounts WHERE id = ?`, accountID).Scan(&email)
	return email
}

// ---------- calendars ----------

func (h *Handler) fetchOutlookCalendarList(ctx context.Context, token, accountEmail string) ([]models.Calendar, error) {
	var out []models.Calendar
	link := outlookCalendarURL("/me/calendars", url.Values{"$top": {"100"}})
	for link != "" {
		var page struct {
			Value    []graphCalendar `json:"value"`
			NextLink string          `json:"@odata.nextLink"`
		}
		if err := outlookCalendarDo(ctx, token, http.MethodGet, link, nil, &page); err != nil {
			return nil, err
		}
		for _, c := range page.Value {
			if c.ID != "" {
				out = append(out, graphCalendarToModel(c, accountEmail))
			}
		}
		if link = page.NextLink; link != "" {
			if err := checkGraphLink(link); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// graphCalendarToModel maps canEdit/owner to the access roles the rest of the
// app uses: read-only calendars are "reader"; editable ones are "owner" when
// they are the user's own (default, or owned by the account) and "writer" when
// shared with edit rights.
func graphCalendarToModel(c graphCalendar, accountEmail string) models.Calendar {
	role := "reader"
	if c.CanEdit {
		role = "writer"
		if c.IsDefaultCalendar || c.Owner.Address == "" || strings.EqualFold(c.Owner.Address, accountEmail) {
			role = "owner"
		}
	}
	color := c.HexColor
	if color == "" {
		color = graphCalendarColors[c.Color]
	}
	if color == "" {
		color = graphDefaultCalendarColor
	}
	return models.Calendar{
		ProviderCalendarID: c.ID, Name: c.Name, Color: color,
		IsPrimary: c.IsDefaultCalendar, AccessRole: role, // Graph calendars carry no time zone
	}
}

func (h *Handler) pullOutlookCalendarAccount(ctx context.Context, accountID string) error {
	token, err := h.mailCredentials().GetMicrosoftGraphCalendarTokenForAccount(ctx, accountID)
	if err != nil {
		return err
	}
	email := h.accountEmail(ctx, accountID)
	calendars, err := h.fetchOutlookCalendarList(ctx, token, email)
	var apiErr outlookAPIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden {
		// Listing calendars is account-level, so a 403 here means the token lacks
		// the scope (Microsoft may issue a token for only the consented scopes).
		return fmt.Errorf("%w: %v", mailauth.ErrMicrosoftCalendarConsentRequired, err)
	}
	if err != nil {
		return err
	}
	if err := h.db.UpsertCalendars(ctx, accountID, calendars); err != nil {
		return err
	}
	selected, err := h.db.ListSelectedCalendarsForAccount(ctx, accountID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, cal := range selected {
		if err := h.syncOutlookCalendarEvents(ctx, token, email, cal); err != nil {
			log.Printf("calendar sync %s calendar %d: %v", accountID, cal.ID, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// ---------- events ----------

func outlookCalendarEventsPath(providerCalendarID string) string {
	return "/me/calendars/" + url.PathEscape(providerCalendarID) + "/calendarView/delta"
}

// syncOutlookCalendarEvents mirrors syncGoogleCalendarEvents: incremental from
// the stored deltaLink while its window is fresh, otherwise (or when Graph
// says the token is gone) a full resync that re-bases the window.
func (h *Handler) syncOutlookCalendarEvents(ctx context.Context, token, selfEmail string, cal models.Calendar) error {
	now := time.Now()
	if cal.SyncToken != "" && cal.WindowStart != nil && cal.WindowEnd != nil &&
		now.Sub(*cal.WindowStart) < calendarInitialPast+calendarWindowMaxAge {
		err := h.syncOutlookCalendarIncremental(ctx, token, selfEmail, cal)
		if !errors.Is(err, errOutlookDeltaExpired) {
			return err
		}
	}
	return h.syncOutlookCalendarFull(ctx, token, selfEmail, cal, now)
}

const outlookDeltaPrefer = "odata.maxpagesize=200"

func (h *Handler) syncOutlookCalendarFull(ctx context.Context, token, selfEmail string, cal models.Calendar, now time.Time) error {
	winStart, winEnd := now.Add(-calendarInitialPast), now.Add(calendarInitialFuture)
	link := outlookCalendarURL(outlookCalendarEventsPath(cal.ProviderCalendarID), url.Values{
		"startDateTime": {winStart.UTC().Format(time.RFC3339)}, "endDateTime": {winEnd.UTC().Format(time.RFC3339)},
	})
	var events []models.CalendarEvent
	for {
		var page graphEventsPage
		if err := outlookCalendarDo(ctx, token, http.MethodGet, link, nil, &page, outlookDeltaPrefer); err != nil {
			return err
		}
		for _, item := range page.Value {
			if item.Removed != nil || item.IsCancelled {
				continue
			}
			ev, err := outlookEventToModel(item, selfEmail)
			if err != nil {
				return fmt.Errorf("event %s: %w", item.ID, err)
			}
			events = append(events, ev)
		}
		if page.NextLink != "" {
			if err := checkGraphLink(page.NextLink); err != nil {
				return err
			}
			link = page.NextLink
			continue
		}
		if page.DeltaLink == "" {
			return errors.New("microsoft graph returned no deltaLink")
		}
		if err := checkGraphLink(page.DeltaLink); err != nil {
			return err
		}
		return h.db.ReplaceCalendarEvents(ctx, cal.ID, cal.AccountID, events, page.DeltaLink, winStart, winEnd)
	}
}

func (h *Handler) syncOutlookCalendarIncremental(ctx context.Context, token, selfEmail string, cal models.Calendar) error {
	link := cal.SyncToken
	for {
		if checkGraphLink(link) != nil {
			return errOutlookDeltaExpired // not a link we issued (e.g. base URL changed): start over
		}
		var page graphEventsPage
		if err := outlookCalendarDo(ctx, token, http.MethodGet, link, nil, &page, outlookDeltaPrefer); err != nil {
			if isOutlookDeltaExpired(err) {
				return errOutlookDeltaExpired
			}
			return err
		}
		var upserts []models.CalendarEvent
		var deletes []string
		for _, item := range page.Value {
			if item.Removed != nil || item.IsCancelled {
				deletes = append(deletes, item.ID)
				continue
			}
			ev, err := outlookEventToModel(item, selfEmail)
			if err != nil {
				return fmt.Errorf("event %s: %w", item.ID, err)
			}
			upserts = append(upserts, ev)
		}
		if err := h.db.ApplyCalendarEventPage(ctx, cal.ID, cal.AccountID, upserts, deletes); err != nil {
			return err
		}
		if page.NextLink != "" {
			link = page.NextLink
			continue
		}
		if page.DeltaLink == "" {
			return errors.New("microsoft graph returned no deltaLink")
		}
		if err := checkGraphLink(page.DeltaLink); err != nil {
			return err
		}
		return h.db.FinishIncrementalCalendarSync(ctx, cal.ID, page.DeltaLink)
	}
}

// parseGraphDateTime returns UTC. Requests ask for UTC, but a zone name is
// honoured if Graph answers in another one.
func parseGraphDateTime(t graphDateTime) (time.Time, error) {
	loc := time.UTC
	if t.TimeZone != "" && !strings.EqualFold(t.TimeZone, "UTC") {
		l, err := time.LoadLocation(t.TimeZone)
		if err != nil {
			return time.Time{}, fmt.Errorf("unknown time zone %q", t.TimeZone)
		}
		loc = l
	}
	parsed, err := time.ParseInLocation("2006-01-02T15:04:05", t.DateTime, loc) // accepts the 7-digit fraction
	if err != nil {
		return time.Time{}, fmt.Errorf("unparseable dateTime %q", t.DateTime)
	}
	return parsed.UTC(), nil
}

// nearestMidnight snaps a UTC instant to the closest 00:00 UTC. All-day events
// are midnight-to-midnight in their own zone; if Graph renders them as a shifted
// UTC instant (zone offset within +-12h) this recovers the intended calendar day.
// ponytail: assumes |UTC offset| < 12h (wrong by a day for NZ summer, Tonga, Samoa,
// Kiritimati); fixing it needs originalStartTimeZone (a Windows name) mapped to IANA.
func nearestMidnight(t time.Time) time.Time {
	d := t.Truncate(24 * time.Hour)
	if t.Sub(d) >= 12*time.Hour {
		d = d.Add(24 * time.Hour)
	}
	return d
}

func graphSelfResponse(r string) string {
	switch r {
	case "accepted", "organizer":
		return "accepted"
	case "declined":
		return "declined"
	case "tentativelyAccepted":
		return "tentative"
	case "notResponded":
		return "needsAction"
	}
	return ""
}

// outlookEventToModel converts a non-removed Graph event. selfEmail (the
// account address) identifies the user's own attendee entry, since Graph has
// no "self" flag. Times are stored in UTC; all-day events keep floating dates
// (end exclusive) with UTC midnights as the sort key, like Google's.
func outlookEventToModel(g graphEvent, selfEmail string) (models.CalendarEvent, error) {
	e := models.CalendarEvent{
		ProviderEventID: g.ID, ICalUID: g.ICalUID, Title: g.Subject, Location: g.Location.DisplayName,
		Status: "confirmed", OrganizerEmail: g.Organizer.EmailAddress.Address, HTMLLink: g.WebLink,
		Attendees: []models.EventAttendee{},
	}
	if g.Type == "occurrence" || g.Type == "exception" {
		e.RecurringEventID = g.SeriesMasterID
	}
	if strings.EqualFold(g.Body.ContentType, "html") {
		e.Description = googleDescriptionText(g.Body.Content)
	} else {
		e.Description = strings.TrimSpace(g.Body.Content)
	}
	// Graph reports the creation zone as a Windows name; keep it only when it is
	// an IANA name the editor can send back as time_zone.
	if _, err := time.LoadLocation(g.OriginalStartTimeZone); err == nil && g.OriginalStartTimeZone != "" {
		e.EventTimeZone = g.OriginalStartTimeZone
	}
	start, err := parseGraphDateTime(g.Start)
	if err != nil {
		return e, fmt.Errorf("start: %w", err)
	}
	end, err := parseGraphDateTime(g.End)
	if err != nil {
		return e, fmt.Errorf("end: %w", err)
	}
	if g.IsAllDay {
		start, end = nearestMidnight(start), nearestMidnight(end)
		if !end.After(start) {
			end = start.AddDate(0, 0, 1)
		}
		e.AllDay = true
		e.StartDate, e.EndDate = start.Format(calendarDateLayout), end.Format(calendarDateLayout)
	}
	e.StartAt, e.EndAt = start, end
	if t, err := time.Parse(time.RFC3339, g.LastModified); err == nil {
		t = t.UTC()
		e.UpdatedAtProvider = &t
	}

	organizer := strings.ToLower(g.Organizer.EmailAddress.Address)
	sawOrganizer := false
	for _, a := range g.Attendees {
		addr := strings.ToLower(a.EmailAddress.Address)
		att := models.EventAttendee{
			Email: a.EmailAddress.Address, Name: a.EmailAddress.Name, Response: graphSelfResponse(a.Status.Response),
			Organizer: addr != "" && addr == organizer, Self: addr != "" && strings.EqualFold(a.EmailAddress.Address, selfEmail),
			Optional: a.Type == "optional", Resource: a.Type == "resource",
		}
		sawOrganizer = sawOrganizer || att.Organizer
		e.Attendees = append(e.Attendees, att)
	}
	// Graph lists the organizer separately; show them with the guests like Google does.
	if len(e.Attendees) > 0 && !sawOrganizer && organizer != "" {
		e.Attendees = append([]models.EventAttendee{{
			Email: g.Organizer.EmailAddress.Address, Name: g.Organizer.EmailAddress.Name, Response: "accepted", Organizer: true,
			Self: strings.EqualFold(g.Organizer.EmailAddress.Address, selfEmail),
		}}, e.Attendees...)
	}
	switch {
	case g.IsOrganizer:
		e.SelfResponse = "accepted"
	default:
		e.SelfResponse = graphSelfResponse(g.ResponseStatus.Response)
		if e.SelfResponse == "" && len(g.Attendees) > 0 {
			e.SelfResponse = "needsAction"
		}
	}
	if g.OnlineMeeting != nil && g.OnlineMeeting.JoinURL != "" {
		e.MeetingURL = g.OnlineMeeting.JoinURL
	} else {
		e.MeetingURL = g.OnlineMeetingURL
	}
	return e, nil
}
