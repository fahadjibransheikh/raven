package handler

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/mail/ical"
	"github.com/cristianadrielbraun/gofer/internal/models"
)

// iCloud Calendar over CalDAV (phase C5). Accounts are iCloud Mail accounts
// (provider imap) using their app-specific password; see
// storage.calendarProviderSQL for how they are recognised.
//
// Standards and Apple behaviour this follows (read 2026-10-04):
//   RFC 4791 CalDAV     calendar-home-set 6.2.1, calendar-query + time-range 7.8/9.9,
//                       calendar-multiget 7.9, If-None-Match/If-Match on PUT 5.3.2
//   RFC 6578            sync-collection REPORT, opaque sync-token, 404 members = removed 3.5.2
//   RFC 5545            RRULE 3.3.10, EXDATE 3.8.5.1, RECURRENCE-ID 3.8.4.4, folding 3.1
//   RFC 6638            attendee PARTSTAT change on PUT makes the server send the iTIP REPLY 3.2.2.3
// Endpoint: https://caldav.icloud.com/ (PROPFIND for current-user-principal),
// the calendar home lives on a per-account pNN-caldav.icloud.com host.
//
// Not verified without a live account (see the report): whether iCloud honours
// <C:expand> (we never send it; recurrences are expanded here), whether it
// answers sync-collection, and whether a bare TZID without VTIMEZONE is accepted.

var icloudCalDAVBaseURL = "https://caldav.icloud.com/"

const (
	icloudMultigetBatch = 50
	icloudMaxBody       = 16 << 20
	icloudReconnectMsg  = "iCloud rejected the app-specific password; update it in Settings > Accounts > Edit"
)

var errICloudSyncTokenInvalid = errors.New("icloud: sync token no longer valid")

type icloudHTTPError struct {
	Status int
	Body   string
}

func (e icloudHTTPError) Error() string {
	return fmt.Sprintf("iCloud CalDAV returned %d: %s", e.Status, strings.TrimSpace(e.Body))
}

func isICloudAuthError(err error) bool {
	var he icloudHTTPError
	return errors.As(err, &he) && he.Status == http.StatusUnauthorized
}

func isICloudStatus(err error, statuses ...int) bool {
	var he icloudHTTPError
	if !errors.As(err, &he) {
		return false
	}
	for _, s := range statuses {
		if he.Status == s {
			return true
		}
	}
	return false
}

// icloudHostAllowed keeps the app-specific password from ever being sent to a
// host the server merely pointed us at.
func icloudHostAllowed(u *url.URL) bool {
	base, err := url.Parse(icloudCalDAVBaseURL)
	if err != nil || u.Scheme != base.Scheme {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return strings.EqualFold(u.Host, base.Host) || h == "icloud.com" || strings.HasSuffix(h, ".icloud.com")
}

type icloudClient struct {
	user, pass string
	http       *http.Client
}

func newICloudClient(user, pass string) *icloudClient {
	return &icloudClient{user: user, pass: pass, http: &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || !icloudHostAllowed(req.URL) {
				return errors.New("icloud: refusing redirect")
			}
			return nil
		},
	}}
}

type davReply struct {
	Status int
	Header http.Header
	Body   []byte
}

// do sends one request; any non-2xx answer is an icloudHTTPError.
func (c *icloudClient) do(ctx context.Context, method, target string, headers map[string]string, body []byte) (davReply, error) {
	u, err := url.Parse(target)
	if err != nil || !icloudHostAllowed(u) {
		return davReply{}, errors.New("icloud: calendar URL is not on iCloud")
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := newCardDAVRequest(ctx, method, target, c.user, c.pass, rd)
	if err != nil {
		return davReply{}, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return davReply{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, icloudMaxBody))
	if err != nil {
		return davReply{}, err
	}
	reply := davReply{Status: resp.StatusCode, Header: resp.Header, Body: data}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := data
		if len(snippet) > 2048 {
			snippet = snippet[:2048]
		}
		return reply, icloudHTTPError{Status: resp.StatusCode, Body: string(snippet)}
	}
	return reply, nil
}

func (c *icloudClient) multistatus(ctx context.Context, method, target, depth, xmlBody string) (davMultiStatus, error) {
	reply, err := c.do(ctx, method, target, map[string]string{"Depth": depth, "Content-Type": `application/xml; charset="utf-8"`}, []byte(xmlBody))
	if err != nil {
		return davMultiStatus{}, err
	}
	return decodeDAVMultiStatus(bytes.NewReader(reply.Body))
}

// ---------- discovery ----------

const (
	nsHeader  = `xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:cs="http://calendarserver.org/ns/" xmlns:ic="http://apple.com/ns/ical/"`
	xmlProlog = `<?xml version="1.0" encoding="utf-8"?>`
)

// discover walks base -> principal -> calendar-home-set -> calendars.
// userAddresses are the principal's calendar-user-address-set (mailto: stripped).
func (c *icloudClient) discover(ctx context.Context) (calendars []models.Calendar, userAddresses []string, err error) {
	base := icloudCalDAVBaseURL
	multi, err := c.multistatus(ctx, "PROPFIND", base, "0", xmlProlog+`<d:propfind `+nsHeader+`><d:prop><d:current-user-principal/></d:prop></d:propfind>`)
	if err != nil {
		return nil, nil, err
	}
	principal := ""
	for _, r := range multi.Responses {
		if h := strings.TrimSpace(r.okProp().CurrentUserPrincipal.Href); h != "" {
			principal = absoluteDAVHref(base, h)
		}
	}
	if principal == "" {
		return nil, nil, errors.New("icloud: no current-user-principal")
	}
	multi, err = c.multistatus(ctx, "PROPFIND", principal, "0", xmlProlog+`<d:propfind `+nsHeader+`><d:prop><c:calendar-home-set/><c:calendar-user-address-set/></d:prop></d:propfind>`)
	if err != nil {
		return nil, nil, err
	}
	home := ""
	for _, r := range multi.Responses {
		p := r.okProp()
		if h := strings.TrimSpace(p.CalendarHomeSet.Href); h != "" {
			home = absoluteDAVHref(principal, h)
		}
		for _, a := range p.CalendarUserAddresses.Hrefs {
			if a = strings.TrimSpace(a); len(a) > 7 && strings.EqualFold(a[:7], "mailto:") {
				userAddresses = append(userAddresses, strings.ToLower(a[7:]))
			}
		}
	}
	if home == "" {
		return nil, nil, errors.New("icloud: no calendar-home-set")
	}
	multi, err = c.multistatus(ctx, "PROPFIND", home, "1", xmlProlog+`<d:propfind `+nsHeader+`><d:prop><d:displayname/><d:resourcetype/><c:supported-calendar-component-set/><ic:calendar-color/><d:current-user-privilege-set/></d:prop></d:propfind>`)
	if err != nil {
		return nil, nil, err
	}
	for _, r := range multi.Responses {
		if cal, ok := icloudCalendarFromResponse(home, r); ok {
			calendars = append(calendars, cal)
		}
	}
	for i := range calendars { // the first calendar the user owns is the default
		if calendars[i].AccessRole == "owner" {
			calendars[i].IsPrimary = true
			break
		}
	}
	return calendars, userAddresses, nil
}

// icloudCalendarFromResponse keeps event calendars only (reminder lists carry
// VTODO alone) and maps privileges: write means owner, or writer for a
// calendar shared to the user; anything else is a reader.
func icloudCalendarFromResponse(home string, r davResponse) (models.Calendar, bool) {
	p := r.okProp()
	if !p.ResourceType.Calendar || r.deleted() {
		return models.Calendar{}, false
	}
	if len(p.CalendarComponents.Names) > 0 && !containsFold(p.CalendarComponents.Names, "VEVENT") {
		return models.Calendar{}, false
	}
	calURL := icloudCollectionURL(absoluteDAVHref(home, r.Href))
	name := strings.TrimSpace(p.DisplayName)
	if name == "" {
		name = cardDAVAddressBookName(calURL)
	}
	color := strings.TrimSpace(p.CalendarColor)
	if len(color) >= 7 && color[0] == '#' {
		color = strings.ToLower(color[:7]) // iCloud sends #RRGGBBAA
	} else {
		color = "#0a84ff"
	}
	role := "reader"
	if containsFold(p.Privileges.Names, "write") || containsFold(p.Privileges.Names, "write-content") || containsFold(p.Privileges.Names, "all") {
		role = "owner"
		if p.ResourceType.Shared {
			role = "writer"
		}
	}
	return models.Calendar{ProviderCalendarID: calURL, Name: name, Color: color, AccessRole: role}, true
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

// icloudCollectionURL gives calendar URLs one stable spelling: no default
// port, trailing slash.
func icloudCollectionURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Port() == "443" && u.Scheme == "https" {
		u.Host = u.Hostname()
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
		u.RawPath = ""
	}
	return u.String()
}

// icloudResourceKey is the stored identity of a resource: its href path.
func icloudResourceKey(href string) string {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return strings.TrimSpace(href)
	}
	return u.EscapedPath()
}

func icloudResourceURL(calURL, key string) string { return absoluteDAVHref(calURL, key) }

// ---------- account plumbing ----------

func (h *Handler) icloudClientFor(ctx context.Context, accountID string) (*icloudClient, error) {
	if h.accountStore == nil {
		return nil, errors.New("icloud: account store unavailable")
	}
	var username, email string
	if err := h.db.Read().QueryRowContext(ctx, `SELECT COALESCE(username, ''), email_address FROM accounts WHERE id = ? AND COALESCE(is_deleting, 0) = 0`, accountID).Scan(&username, &email); err != nil {
		return nil, err
	}
	if strings.TrimSpace(username) == "" {
		username = email
	}
	pass, err := h.accountStore.DecryptPassword(ctx, accountID)
	if err != nil || pass == "" {
		return nil, fmt.Errorf("icloud: no app-specific password stored for account")
	}
	return newICloudClient(strings.TrimSpace(username), pass), nil
}

// icloudSelfAddresses are the addresses that count as "me" on attendee lines.
func (h *Handler) icloudSelfAddresses(ctx context.Context, accountID string, extra []string) map[string]bool {
	self := map[string]bool{}
	add := func(s string) {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			self[s] = true
		}
	}
	add(h.accountEmail(ctx, accountID))
	for _, a := range extra {
		add(a)
	}
	rows, err := h.db.Read().QueryContext(ctx, `SELECT email FROM account_identities WHERE account_id = ?`, accountID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var e string
			if rows.Scan(&e) == nil {
				add(e)
			}
		}
	}
	return self
}

// ---------- sync ----------

func (h *Handler) pullICloudCalendarAccount(ctx context.Context, accountID string) error {
	c, err := h.icloudClientFor(ctx, accountID)
	if err != nil {
		return err
	}
	calendars, userAddrs, err := c.discover(ctx)
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
	self := h.icloudSelfAddresses(ctx, accountID, userAddrs)
	var firstErr error
	for _, cal := range selected {
		if err := h.syncICloudCalendar(ctx, c, self, cal); err != nil {
			if isICloudAuthError(err) {
				return err
			}
			log.Printf("calendar sync %s calendar %d: %v", accountID, cal.ID, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// syncICloudCalendar is incremental from the stored sync-token while its
// window is fresh, otherwise (or when the token is rejected, or the server
// never gave one) a full window resync: calendar-query over the window with
// the whole set replaced. The task asked for "fallback = time-range query plus
// ETag comparison"; without a stored per-resource ETag (that would need a
// migration) the fallback replaces the window instead.
func (h *Handler) syncICloudCalendar(ctx context.Context, c *icloudClient, self map[string]bool, cal models.Calendar) error {
	now := time.Now()
	if cal.SyncToken != "" && cal.WindowStart != nil && cal.WindowEnd != nil &&
		now.Sub(*cal.WindowStart) < calendarInitialPast+calendarWindowMaxAge {
		err := h.syncICloudIncremental(ctx, c, self, cal)
		if !errors.Is(err, errICloudSyncTokenInvalid) {
			return err
		}
	}
	return h.syncICloudFull(ctx, c, self, cal, now)
}

type icloudSyncResult struct {
	Token   string
	Changed []string // resource hrefs (changed or new)
	Removed []string
}

// syncCollection is RFC 6578 section 3: an empty token lists every member.
func (c *icloudClient) syncCollection(ctx context.Context, calURL, token string) (icloudSyncResult, error) {
	body := xmlProlog + `<d:sync-collection ` + nsHeader + `><d:sync-token>` + xmlEscapeText(token) + `</d:sync-token><d:sync-level>1</d:sync-level><d:prop><d:getetag/></d:prop></d:sync-collection>`
	reply, err := c.do(ctx, "REPORT", calURL, map[string]string{"Depth": "0", "Content-Type": `application/xml; charset="utf-8"`}, []byte(body))
	if err != nil {
		var he icloudHTTPError
		if errors.As(err, &he) && token != "" && (he.Status == http.StatusGone || he.Status == http.StatusConflict ||
			(he.Status == http.StatusForbidden && strings.Contains(he.Body, "valid-sync-token"))) {
			return icloudSyncResult{}, errICloudSyncTokenInvalid
		}
		return icloudSyncResult{}, err
	}
	multi, err := decodeDAVMultiStatus(bytes.NewReader(reply.Body))
	if err != nil {
		return icloudSyncResult{}, err
	}
	res := icloudSyncResult{Token: strings.TrimSpace(multi.SyncToken)}
	collection := icloudResourceKey(calURL)
	for _, r := range multi.Responses {
		key := icloudResourceKey(r.Href)
		if key == "" || strings.TrimSuffix(key, "/") == strings.TrimSuffix(collection, "/") || strings.HasSuffix(key, "/") {
			continue
		}
		if r.deleted() {
			res.Removed = append(res.Removed, key)
		} else {
			res.Changed = append(res.Changed, key)
		}
	}
	return res, nil
}

func icloudTimeRange(t time.Time) string { return t.UTC().Format("20060102T150405Z") }

type icloudResource struct {
	Key  string
	Data []byte
}

func resourcesFromMultistatus(multi davMultiStatus) (have []icloudResource, missing, gone []string) {
	for _, r := range multi.Responses {
		key := icloudResourceKey(r.Href)
		if key == "" || strings.HasSuffix(key, "/") {
			continue
		}
		switch data := strings.TrimSpace(r.okProp().CalendarData); {
		case r.deleted():
			gone = append(gone, key)
		case data != "":
			have = append(have, icloudResource{Key: key, Data: []byte(r.okProp().CalendarData)})
		default:
			missing = append(missing, key)
		}
	}
	return
}

// queryWindow is calendar-query with a time-range (RFC 4791 7.8): recurring
// resources come back as their master when any instance overlaps the window.
func (c *icloudClient) queryWindow(ctx context.Context, calURL string, from, to time.Time) ([]icloudResource, error) {
	body := xmlProlog + `<c:calendar-query ` + nsHeader + `><d:prop><d:getetag/><c:calendar-data/></d:prop><c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VEVENT"><c:time-range start="` +
		icloudTimeRange(from) + `" end="` + icloudTimeRange(to) + `"/></c:comp-filter></c:comp-filter></c:filter></c:calendar-query>`
	multi, err := c.multistatus(ctx, "REPORT", calURL, "1", body)
	if err != nil {
		return nil, err
	}
	have, missing, _ := resourcesFromMultistatus(multi)
	if len(missing) > 0 { // a server that answers etags only
		more, _, err := c.multiget(ctx, calURL, missing)
		if err != nil {
			return nil, err
		}
		have = append(have, more...)
	}
	return have, nil
}

// multiget is calendar-multiget (RFC 4791 7.9), in batches. gone lists
// requested resources the server no longer has.
func (c *icloudClient) multiget(ctx context.Context, calURL string, keys []string) (have []icloudResource, gone []string, err error) {
	for start := 0; start < len(keys); start += icloudMultigetBatch {
		end := min(start+icloudMultigetBatch, len(keys))
		var b strings.Builder
		b.WriteString(xmlProlog + `<c:calendar-multiget ` + nsHeader + `><d:prop><d:getetag/><c:calendar-data/></d:prop>`)
		for _, k := range keys[start:end] {
			b.WriteString(`<d:href>` + xmlEscapeText(k) + `</d:href>`)
		}
		b.WriteString(`</c:calendar-multiget>`)
		multi, err := c.multistatus(ctx, "REPORT", calURL, "1", b.String())
		if err != nil {
			return nil, nil, err
		}
		got, missing, deleted := resourcesFromMultistatus(multi)
		have = append(have, got...)
		gone = append(gone, deleted...)
		gone = append(gone, missing...)
	}
	return have, gone, nil
}

func (h *Handler) syncICloudFull(ctx context.Context, c *icloudClient, self map[string]bool, cal models.Calendar, now time.Time) error {
	winStart, winEnd := now.Add(-calendarInitialPast), now.Add(calendarInitialFuture)
	// Take the token before reading: a change made in between is then replayed
	// by the next incremental sync (idempotent) instead of being missed.
	token := ""
	if res, err := c.syncCollection(ctx, cal.ProviderCalendarID, ""); err == nil {
		token = res.Token
	} else if isICloudAuthError(err) {
		return err
	} else {
		log.Printf("calendar sync calendar %d: no sync-token, will resync the window next time: %v", cal.ID, err)
	}
	resources, err := c.queryWindow(ctx, cal.ProviderCalendarID, winStart, winEnd)
	if err != nil {
		return err
	}
	var events []models.CalendarEvent
	for _, r := range resources {
		evs, err := icloudResourceEvents(r.Key, r.Data, self, h.icloudFloatingZone(ctx, cal), winStart, winEnd)
		if err != nil {
			log.Printf("calendar sync calendar %d: skip resource: %v", cal.ID, err)
			continue
		}
		events = append(events, evs...)
	}
	return h.db.ReplaceCalendarEvents(ctx, cal.ID, cal.AccountID, events, token, winStart, winEnd)
}

func (h *Handler) syncICloudIncremental(ctx context.Context, c *icloudClient, self map[string]bool, cal models.Calendar) error {
	res, err := c.syncCollection(ctx, cal.ProviderCalendarID, cal.SyncToken)
	if err != nil {
		return err
	}
	have, gone, err := c.multiget(ctx, cal.ProviderCalendarID, res.Changed)
	if err != nil {
		return err
	}
	var events []models.CalendarEvent
	hrefs := append(append([]string(nil), res.Removed...), gone...)
	for _, r := range have {
		hrefs = append(hrefs, r.Key)
		evs, err := icloudResourceEvents(r.Key, r.Data, self, h.icloudFloatingZone(ctx, cal), *cal.WindowStart, *cal.WindowEnd)
		if err != nil {
			log.Printf("calendar sync calendar %d: skip resource: %v", cal.ID, err)
			continue
		}
		events = append(events, evs...)
	}
	if err := h.db.ApplyCalendarResources(ctx, cal.ID, cal.AccountID, hrefs, events); err != nil {
		return err
	}
	token := res.Token
	if token == "" {
		token = cal.SyncToken
	}
	return h.db.FinishIncrementalCalendarSync(ctx, cal.ID, token)
}

// icloudFloatingZone is the zone for floating (zone-less) times: the owner's
// timezone setting. Syncs run without a request user, so it is looked up by account.
func (h *Handler) icloudFloatingZone(ctx context.Context, cal models.Calendar) *time.Location {
	var userID string
	if err := h.db.Read().QueryRowContext(ctx, `SELECT user_id FROM accounts WHERE id = ?`, cal.AccountID).Scan(&userID); err == nil {
		if tz := strings.TrimSpace(h.db.GetUISettings(ctx, userID)["timezone"]); tz != "" && tz != "local" {
			if l, err := time.LoadLocation(tz); err == nil {
				return l
			}
		}
	}
	if l, err := time.LoadLocation(cal.TimeZone); err == nil && cal.TimeZone != "" {
		return l
	}
	return time.UTC
}

// ---------- iCalendar -> model ----------

const icloudRecIDLayout = "20060102T150405Z"

// icloudEventID is the stable provider_event_id: the resource href for a plain
// event, "<href>#<recurrence-id>" for an occurrence (UTC, or a date when all-day).
func icloudEventID(key string, recID time.Time, allDay bool) string {
	if recID.IsZero() {
		return key
	}
	if allDay {
		return key + "#" + recID.Format("20060102")
	}
	return key + "#" + recID.UTC().Format(icloudRecIDLayout)
}

func icloudResponse(partStat string) string {
	switch partStat {
	case "ACCEPTED":
		return "accepted"
	case "DECLINED":
		return "declined"
	case "TENTATIVE":
		return "tentative"
	}
	return "needsAction"
}

// icloudResourceEvents expands one .ics into stored instances overlapping the window.
func icloudResourceEvents(key string, data []byte, self map[string]bool, floating *time.Location, from, to time.Time) ([]models.CalendarEvent, error) {
	res, err := ical.ParseResource(data, floating)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	insts, err := res.Instances(from, to)
	if err != nil {
		// Unsupported rule: the first occurrence is kept (see ical.Instances).
		log.Printf("calendar sync: %s: %v; showing only the first occurrence", key, err)
	}
	var out []models.CalendarEvent
	for _, in := range insts {
		out = append(out, icloudInstanceToModel(key, in, self))
	}
	return out, nil
}

func icloudInstanceToModel(key string, in ical.Instance, self map[string]bool) models.CalendarEvent {
	e := in.Event
	ev := models.CalendarEvent{
		ProviderEventID: icloudEventID(key, in.RecurrenceID, e.AllDay), ICalUID: e.UID,
		Title: e.Summary, Location: e.Location, Description: strings.TrimSpace(e.Description),
		StartAt: in.Start.UTC(), EndAt: in.End.UTC(), AllDay: e.AllDay, EventTimeZone: e.TZID,
		Status: strings.ToLower(e.Status), OrganizerEmail: e.Organizer.Email, MeetingURL: e.MeetingURL,
		Attendees: []models.EventAttendee{},
	}
	if !in.RecurrenceID.IsZero() {
		ev.RecurringEventID = key
	}
	if ev.Status == "" {
		ev.Status = "confirmed"
	}
	if e.AllDay {
		ev.StartDate, ev.EndDate = in.Start.Format(calendarDateLayout), in.End.Format(calendarDateLayout)
	}
	if !e.LastModified.IsZero() {
		t := e.LastModified.UTC()
		ev.UpdatedAtProvider = &t
	}
	organizer := strings.ToLower(e.Organizer.Email)
	sawOrganizer := false
	for _, a := range e.Attendees {
		addr := strings.ToLower(a.Email)
		att := models.EventAttendee{
			Email: a.Email, Name: a.Name, Response: icloudResponse(a.PartStat),
			Organizer: addr != "" && addr == organizer, Self: addr != "" && self[addr],
		}
		sawOrganizer = sawOrganizer || att.Organizer
		if att.Self {
			ev.SelfResponse = att.Response
		}
		ev.Attendees = append(ev.Attendees, att)
	}
	if len(ev.Attendees) > 0 && !sawOrganizer && organizer != "" {
		org := models.EventAttendee{Email: e.Organizer.Email, Name: e.Organizer.Name, Response: "accepted", Organizer: true, Self: self[organizer]}
		ev.Attendees = append([]models.EventAttendee{org}, ev.Attendees...)
		if org.Self && ev.SelfResponse == "" {
			ev.SelfResponse = "accepted"
		}
	}
	return ev
}

// davHrefsProp is a property holding several <href> children.
type davHrefsProp struct {
	Hrefs []string `xml:"href"`
}

// davNamedItems collects what a property lists: the name="" of <comp> children
// (supported-calendar-component-set) and the local names of every other child
// element (current-user-privilege-set: privilege, read, write, all, ...).
type davNamedItems struct{ Names []string }

func (n *davNamedItems) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "comp" {
				for _, a := range t.Attr {
					if a.Name.Local == "name" {
						n.Names = append(n.Names, a.Value)
					}
				}
			} else {
				n.Names = append(n.Names, t.Name.Local)
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}
