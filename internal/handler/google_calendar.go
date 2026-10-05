package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

var googleCalendarAPIBaseURL = "https://www.googleapis.com/calendar/v3"

const (
	calendarDateLayout    = "2006-01-02"
	calendarInitialPast   = 60 * 24 * time.Hour
	calendarInitialFuture = 365 * 24 * time.Hour
	// The window is fixed when the sync token is issued, so it is re-based
	// (full resync) once it has drifted this far, keeping "now" inside it with
	// at least ~335 days of future coverage.
	calendarWindowMaxAge = 30 * 24 * time.Hour
)

type googleCalendarListResponse struct {
	Items         []googleCalendarListEntry `json:"items"`
	NextPageToken string                    `json:"nextPageToken"`
}

type googleCalendarListEntry struct {
	ID              string `json:"id"`
	Summary         string `json:"summary"`
	SummaryOverride string `json:"summaryOverride"`
	BackgroundColor string `json:"backgroundColor"`
	TimeZone        string `json:"timeZone"`
	AccessRole      string `json:"accessRole"`
	Primary         bool   `json:"primary"`
	Deleted         bool   `json:"deleted"`
}

type googleEventTime struct {
	Date     string `json:"date"`
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

type googleCalendarEvent struct {
	ID               string          `json:"id"`
	Status           string          `json:"status"`
	ICalUID          string          `json:"iCalUID"`
	RecurringEventID string          `json:"recurringEventId"`
	Summary          string          `json:"summary"`
	Location         string          `json:"location"`
	Description      string          `json:"description"`
	HTMLLink         string          `json:"htmlLink"`
	HangoutLink      string          `json:"hangoutLink"`
	Updated          string          `json:"updated"`
	Start            googleEventTime `json:"start"`
	End              googleEventTime `json:"end"`
	Organizer        struct {
		Email string `json:"email"`
		Self  bool   `json:"self"`
	} `json:"organizer"`
	Attendees []struct {
		Email          string `json:"email"`
		DisplayName    string `json:"displayName"`
		ResponseStatus string `json:"responseStatus"`
		Organizer      bool   `json:"organizer"`
		Self           bool   `json:"self"`
		Optional       bool   `json:"optional"`
		Resource       bool   `json:"resource"`
	} `json:"attendees"`
	ConferenceData struct {
		EntryPoints []struct {
			EntryPointType string `json:"entryPointType"`
			URI            string `json:"uri"`
		} `json:"entryPoints"`
	} `json:"conferenceData"`
}

type googleCalendarEventsResponse struct {
	Items         []googleCalendarEvent `json:"items"`
	NextPageToken string                `json:"nextPageToken"`
	NextSyncToken string                `json:"nextSyncToken"`
}

var errGoogleSyncTokenExpired = errors.New("google calendar sync token expired")

func googleCalendarGet(ctx context.Context, accessToken, path string, values url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleCalendarAPIBaseURL+path+"?"+values.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return newGoogleAPIError(resp, body)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// isGoogleCalendarScopeError reports a 403 caused by the token lacking the
// Calendar scopes (accounts connected before calendar support).
func isGoogleCalendarScopeError(err error) bool {
	var apiErr googleAPIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		return false
	}
	return strings.Contains(apiErr.Body, "insufficientPermissions") ||
		strings.Contains(apiErr.Body, "ACCESS_TOKEN_SCOPE_INSUFFICIENT") ||
		strings.Contains(strings.ToLower(apiErr.Body), "insufficient authentication scopes")
}

func (h *Handler) fetchGoogleCalendarList(ctx context.Context, accessToken string) ([]models.Calendar, error) {
	var out []models.Calendar
	pageToken := ""
	for {
		v := url.Values{}
		v.Set("maxResults", "250")
		v.Set("minAccessRole", "reader") // free/busy-only calendars have no event details
		if pageToken != "" {
			v.Set("pageToken", pageToken)
		}
		var page googleCalendarListResponse
		if err := googleCalendarGet(ctx, accessToken, "/users/me/calendarList", v, &page); err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			if item.Deleted || item.ID == "" {
				continue
			}
			name := item.SummaryOverride
			if name == "" {
				name = item.Summary
			}
			out = append(out, models.Calendar{
				ProviderCalendarID: item.ID, Name: name, Color: item.BackgroundColor, TimeZone: item.TimeZone,
				IsPrimary: item.Primary, AccessRole: item.AccessRole,
			})
		}
		if pageToken = page.NextPageToken; pageToken == "" {
			return out, nil
		}
	}
}

// syncGoogleCalendarEvents brings one calendar up to date. Incremental syncs
// send only syncToken (+ the same singleEvents flag): Google rejects timeMin,
// timeMax, updatedMin, orderBy, q, iCalUID and extended-property filters
// together with a syncToken, so the window is fixed at the initial sync and
// remembered on the calendar row.
func (h *Handler) syncGoogleCalendarEvents(ctx context.Context, accessToken string, cal models.Calendar) error {
	now := time.Now()
	if cal.SyncToken != "" && cal.WindowStart != nil && cal.WindowEnd != nil &&
		now.Sub(*cal.WindowStart) < calendarInitialPast+calendarWindowMaxAge {
		err := h.syncGoogleCalendarIncremental(ctx, accessToken, cal)
		if !errors.Is(err, errGoogleSyncTokenExpired) {
			return err
		}
	}
	return h.syncGoogleCalendarFull(ctx, accessToken, cal, now)
}

func (h *Handler) syncGoogleCalendarFull(ctx context.Context, accessToken string, cal models.Calendar, now time.Time) error {
	winStart, winEnd := now.Add(-calendarInitialPast), now.Add(calendarInitialFuture)
	var events []models.CalendarEvent
	pageToken, syncToken := "", ""
	for {
		v := url.Values{}
		v.Set("singleEvents", "true")
		v.Set("maxResults", "2500")
		v.Set("timeMin", winStart.UTC().Format(time.RFC3339))
		v.Set("timeMax", winEnd.UTC().Format(time.RFC3339))
		if pageToken != "" {
			v.Set("pageToken", pageToken)
		}
		var page googleCalendarEventsResponse
		if err := googleCalendarGet(ctx, accessToken, "/calendars/"+url.PathEscape(cal.ProviderCalendarID)+"/events", v, &page); err != nil {
			return err
		}
		for _, item := range page.Items {
			if item.Status == "cancelled" {
				continue
			}
			ev, err := googleEventToModel(item)
			if err != nil {
				return fmt.Errorf("event %s: %w", item.ID, err)
			}
			events = append(events, ev)
		}
		if pageToken = page.NextPageToken; pageToken == "" {
			syncToken = page.NextSyncToken
			break
		}
	}
	if syncToken == "" {
		return errors.New("google calendar returned no nextSyncToken")
	}
	return h.db.ReplaceCalendarEvents(ctx, cal.ID, cal.AccountID, events, syncToken, winStart, winEnd)
}

func (h *Handler) syncGoogleCalendarIncremental(ctx context.Context, accessToken string, cal models.Calendar) error {
	pageToken := ""
	for {
		v := url.Values{}
		v.Set("singleEvents", "true")
		v.Set("maxResults", "2500")
		if pageToken != "" {
			v.Set("pageToken", pageToken)
		} else {
			v.Set("syncToken", cal.SyncToken)
		}
		var page googleCalendarEventsResponse
		err := googleCalendarGet(ctx, accessToken, "/calendars/"+url.PathEscape(cal.ProviderCalendarID)+"/events", v, &page)
		var apiErr googleAPIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusGone {
			return errGoogleSyncTokenExpired
		}
		if err != nil {
			return err
		}
		var upserts []models.CalendarEvent
		var deletes []string
		for _, item := range page.Items {
			if item.Status == "cancelled" {
				deletes = append(deletes, item.ID)
				continue
			}
			ev, err := googleEventToModel(item)
			if err != nil {
				return fmt.Errorf("event %s: %w", item.ID, err)
			}
			upserts = append(upserts, ev)
		}
		if err := h.db.ApplyCalendarEventPage(ctx, cal.ID, cal.AccountID, upserts, deletes); err != nil {
			return err
		}
		if pageToken = page.NextPageToken; pageToken == "" {
			if page.NextSyncToken == "" {
				return errors.New("google calendar returned no nextSyncToken")
			}
			return h.db.FinishIncrementalCalendarSync(ctx, cal.ID, page.NextSyncToken)
		}
	}
}

// googleEventToModel converts a non-cancelled Google event. Timed events are
// normalised to UTC; all-day events keep their floating dates, with UTC
// midnights as a sort key.
func googleEventToModel(g googleCalendarEvent) (models.CalendarEvent, error) {
	e := models.CalendarEvent{
		ProviderEventID: g.ID, ICalUID: g.ICalUID, RecurringEventID: g.RecurringEventID,
		Title: g.Summary, Location: g.Location, Description: googleDescriptionText(g.Description),
		Status: g.Status, OrganizerEmail: g.Organizer.Email, HTMLLink: g.HTMLLink,
		EventTimeZone: g.Start.TimeZone, Attendees: []models.EventAttendee{},
	}
	if e.Status == "" {
		e.Status = "confirmed"
	}
	if g.Start.Date != "" {
		start, err := time.Parse(calendarDateLayout, g.Start.Date)
		if err != nil {
			return e, fmt.Errorf("start date: %w", err)
		}
		end := start.AddDate(0, 0, 1)
		if g.End.Date != "" {
			if end, err = time.Parse(calendarDateLayout, g.End.Date); err != nil {
				return e, fmt.Errorf("end date: %w", err)
			}
		}
		e.AllDay, e.StartAt, e.EndAt = true, start, end
		e.StartDate, e.EndDate = start.Format(calendarDateLayout), end.Format(calendarDateLayout)
	} else {
		start, err := parseGoogleDateTime(g.Start)
		if err != nil {
			return e, fmt.Errorf("start: %w", err)
		}
		end := start
		if g.End.DateTime != "" {
			if end, err = parseGoogleDateTime(g.End); err != nil {
				return e, fmt.Errorf("end: %w", err)
			}
		}
		e.StartAt, e.EndAt = start, end
	}
	if t, err := time.Parse(time.RFC3339, g.Updated); err == nil {
		t = t.UTC()
		e.UpdatedAtProvider = &t
	}
	for _, a := range g.Attendees {
		e.Attendees = append(e.Attendees, models.EventAttendee{
			Email: a.Email, Name: a.DisplayName, Response: a.ResponseStatus,
			Organizer: a.Organizer, Self: a.Self, Optional: a.Optional, Resource: a.Resource,
		})
		if a.Self {
			e.SelfResponse = a.ResponseStatus
		}
	}
	if e.SelfResponse == "" && g.Organizer.Self {
		e.SelfResponse = "accepted"
	}
	e.MeetingURL = g.HangoutLink
	if e.MeetingURL == "" {
		for _, ep := range g.ConferenceData.EntryPoints {
			if ep.EntryPointType == "video" && ep.URI != "" {
				e.MeetingURL = ep.URI
				break
			}
		}
	}
	return e, nil
}

// parseGoogleDateTime returns UTC. Google normally includes an offset; if it
// omits one, timeZone supplies the zone.
func parseGoogleDateTime(t googleEventTime) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, t.DateTime); err == nil {
		return parsed.UTC(), nil
	}
	loc, err := time.LoadLocation(t.TimeZone)
	if err != nil || t.TimeZone == "" {
		return time.Time{}, fmt.Errorf("unparseable dateTime %q", t.DateTime)
	}
	parsed, err := time.ParseInLocation("2006-01-02T15:04:05", t.DateTime, loc)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

var (
	googleDescBreakPattern = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</li>`)
	googleDescTagPattern   = regexp.MustCompile(`<[^>]*>`)
)

// googleDescriptionText flattens Google's HTML-ish descriptions to plain text.
func googleDescriptionText(s string) string {
	if !strings.Contains(s, "<") && !strings.Contains(s, "&") {
		return s
	}
	s = googleDescBreakPattern.ReplaceAllString(s, "\n")
	s = googleDescTagPattern.ReplaceAllString(s, "")
	return strings.TrimSpace(html.UnescapeString(s))
}
