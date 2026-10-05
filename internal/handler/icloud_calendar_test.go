package handler

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/config"
	"github.com/cristianadrielbraun/gofer/internal/mail/ical"
	"github.com/cristianadrielbraun/gofer/internal/models"
	"github.com/cristianadrielbraun/gofer/internal/storage"
)

// fakeCalDAV is a small iCloud-shaped CalDAV server: discovery, calendar list,
// sync-collection (with tokens), calendar-query, calendar-multiget, and
// GET/PUT/DELETE with ETag preconditions. Every request is recorded.
type fakeCalDAV struct {
	t    *testing.T
	mu   sync.Mutex
	srv  *httptest.Server
	reqs []davRec

	user, pass string
	res        map[string]*fakeRes
	n          int // change counter; the sync-token is "tok-<n>"
	changes    []fakeChange

	flipPath    string // after one GET of this path the resource changes (a racing writer)
	noSync      bool   // sync-collection answers 501
	expireToken bool   // any non-empty token is rejected with valid-sync-token
	unauth      bool   // every request answers 401
}

type davRec struct {
	Method, Path, Depth, IfMatch, IfNoneMatch, Auth string
	Body                                            string
}

type fakeRes struct {
	etag string
	data string
}

type fakeChange struct {
	n       int
	path    string
	deleted bool
}

const fakeHome = "/1/calendars/"
const fakeHomeCal = fakeHome + "home/"

func newFakeCalDAV(t *testing.T) *fakeCalDAV {
	f := &fakeCalDAV{t: t, user: "me@icloud.com", pass: "abcd-efgh-ijkl-mnop", res: map[string]*fakeRes{}}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	prev := icloudCalDAVBaseURL
	icloudCalDAVBaseURL = f.srv.URL + "/"
	t.Cleanup(func() { icloudCalDAVBaseURL = prev })
	return f
}

func (f *fakeCalDAV) set(path, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	f.res[path] = &fakeRes{etag: fmt.Sprintf(`"e%d"`, f.n), data: data}
	f.changes = append(f.changes, fakeChange{f.n, path, false})
}

func (f *fakeCalDAV) remove(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	delete(f.res, path)
	f.changes = append(f.changes, fakeChange{f.n, path, true})
}

func (f *fakeCalDAV) get(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r := f.res[path]; r != nil {
		return r.data
	}
	return ""
}

func (f *fakeCalDAV) requests(method string) []davRec {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []davRec
	for _, r := range f.reqs {
		if r.Method == method {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeCalDAV) reportsOf(kind string) []davRec {
	var out []davRec
	for _, r := range f.requests("REPORT") {
		if strings.Contains(r.Body, "<c:"+kind) || strings.Contains(r.Body, "<d:"+kind) {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeCalDAV) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

const msOpen = `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:cs="http://calendarserver.org/ns/" xmlns:ic="http://apple.com/ns/ical/">`

func propResp(href, props string) string {
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop>` + props + `</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func (f *fakeCalDAV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	u, p, _ := r.BasicAuth()
	f.reqs = append(f.reqs, davRec{r.Method, r.URL.Path, r.Header.Get("Depth"), r.Header.Get("If-Match"), r.Header.Get("If-None-Match"), u + ":" + p, string(body)})
	if f.unauth || u != f.user || p != f.pass {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	multi := func(xmlBody string) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = io.WriteString(w, msOpen+xmlBody+`</d:multistatus>`)
	}
	path := r.URL.Path
	switch {
	case r.Method == "PROPFIND" && path == "/":
		multi(propResp("/", `<d:current-user-principal><d:href>/1/principal/</d:href></d:current-user-principal>`))
	case r.Method == "PROPFIND" && path == "/1/principal/":
		multi(propResp(path, `<c:calendar-home-set><d:href>`+f.srv.URL+fakeHome+`</d:href></c:calendar-home-set>`+
			`<c:calendar-user-address-set><d:href>mailto:me@icloud.com</d:href><d:href>mailto:alias@icloud.com</d:href></c:calendar-user-address-set>`))
	case r.Method == "PROPFIND" && path == fakeHome:
		cal := func(name, display, color, comps, privs, extraType string) string {
			return propResp(fakeHome+name+"/", `<d:displayname>`+display+`</d:displayname><d:resourcetype><d:collection/><c:calendar/>`+extraType+`</d:resourcetype>`+
				`<c:supported-calendar-component-set>`+comps+`</c:supported-calendar-component-set><ic:calendar-color>`+color+`</ic:calendar-color>`+
				`<d:current-user-privilege-set>`+privs+`</d:current-user-privilege-set>`)
		}
		rw := `<d:privilege><d:read/></d:privilege><d:privilege><d:write/></d:privilege>`
		multi(propResp(fakeHome, `<d:resourcetype><d:collection/></d:resourcetype>`) +
			cal("home", "Home", "#FF2968FF", `<c:comp name="VEVENT"/>`, rw, "") +
			cal("work", "Work (shared)", "#1BADF8FF", `<c:comp name="VEVENT"/>`, `<d:privilege><d:read/></d:privilege>`, "<cs:shared/>") +
			cal("shared-rw", "Family", "", `<c:comp name="VEVENT"/>`, rw, "<cs:shared/>") +
			cal("tasks", "Reminders", "#63DA38FF", `<c:comp name="VTODO"/>`, rw, ""))
	case r.Method == "REPORT" && strings.HasPrefix(path, fakeHome):
		f.report(w, path, string(body), multi)
	case r.Method == http.MethodGet:
		res := f.res[path]
		if res == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("ETag", res.etag)
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = io.WriteString(w, res.data)
		if f.flipPath == path {
			f.flipPath = ""
			f.n++
			f.res[path] = &fakeRes{etag: fmt.Sprintf(`"e%d"`, f.n), data: strings.ReplaceAll(res.data, "SUMMARY:Solo", "SUMMARY:Elsewhere")}
			f.changes = append(f.changes, fakeChange{f.n, path, false})
		}
	case r.Method == http.MethodPut:
		res := f.res[path]
		if m := r.Header.Get("If-Match"); m != "" && (res == nil || res.etag != m) {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		if r.Header.Get("If-None-Match") == "*" && res != nil {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		f.n++
		f.res[path] = &fakeRes{etag: fmt.Sprintf(`"e%d"`, f.n), data: string(body)}
		f.changes = append(f.changes, fakeChange{f.n, path, false})
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodDelete:
		res := f.res[path]
		if res == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if m := r.Header.Get("If-Match"); m != "" && res.etag != m {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		f.n++
		delete(f.res, path)
		f.changes = append(f.changes, fakeChange{f.n, path, true})
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotImplemented)
	}
}

var tokenRe = regexp.MustCompile(`<d:sync-token>([^<]*)</d:sync-token>`)
var hrefRe = regexp.MustCompile(`<d:href>([^<]*)</d:href>`)

func (f *fakeCalDAV) report(w http.ResponseWriter, path, body string, multi func(string)) {
	collection := path
	withData := func(p string, res *fakeRes) string {
		return propResp(p, `<d:getetag>`+res.etag+`</d:getetag><c:calendar-data>`+xmlText(res.data)+`</c:calendar-data>`)
	}
	switch {
	case strings.Contains(body, "sync-collection"):
		if f.noSync {
			w.WriteHeader(http.StatusNotImplemented)
			return
		}
		tok := tokenRe.FindStringSubmatch(body)[1]
		if tok != "" && f.expireToken {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `<d:error xmlns:d="DAV:"><d:valid-sync-token/></d:error>`)
			return
		}
		since := 0
		if tok != "" {
			since, _ = strconv.Atoi(strings.TrimPrefix(tok, "tok-"))
		}
		var out strings.Builder
		seen := map[string]bool{}
		for i := len(f.changes) - 1; i >= 0; i-- {
			c := f.changes[i]
			if c.n <= since || seen[c.path] || !strings.HasPrefix(c.path, collection) {
				continue
			}
			seen[c.path] = true
			if c.deleted {
				out.WriteString(`<d:response><d:href>` + c.path + `</d:href><d:status>HTTP/1.1 404 Not Found</d:status></d:response>`)
			} else {
				out.WriteString(propResp(c.path, `<d:getetag>`+f.res[c.path].etag+`</d:getetag>`))
			}
		}
		multi(out.String() + `<d:sync-token>tok-` + strconv.Itoa(f.n) + `</d:sync-token>`)
	case strings.Contains(body, "calendar-query"):
		var out strings.Builder
		for _, p := range f.sortedPaths(collection) {
			out.WriteString(withData(p, f.res[p]))
		}
		multi(out.String())
	case strings.Contains(body, "calendar-multiget"):
		var out strings.Builder
		for _, m := range hrefRe.FindAllStringSubmatch(body, -1) {
			if res := f.res[m[1]]; res != nil {
				out.WriteString(withData(m[1], res))
			} else {
				out.WriteString(`<d:response><d:href>` + m[1] + `</d:href><d:status>HTTP/1.1 404 Not Found</d:status></d:response>`)
			}
		}
		multi(out.String())
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *fakeCalDAV) sortedPaths(prefix string) []string {
	var out []string
	for p := range f.res {
		if strings.HasPrefix(p, prefix) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// ---------- fixtures ----------

type icloudFixture struct {
	t    *testing.T
	h    *Handler
	db   *storage.DB
	dav  *fakeCalDAV
	acct string
	day  time.Time // midnight UTC, three days ahead
}

func newICloudFixture(t *testing.T) *icloudFixture {
	t.Helper()
	ctx := context.Background()
	h, db := newGmailAPITestHandler(t, ctx)
	store, err := config.NewAccountStore(db, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	h.accountStore = store
	acc, err := store.CreateAccount(ctx, "default", &models.CreateAccountRequest{
		Provider: "imap", EmailAddress: "me@icloud.com", DisplayName: "Me", IMAPHost: "imap.mail.me.com", IMAPPort: 993, IMAPTLSMode: "tls",
		SMTPHost: "smtp.mail.me.com", SMTPPort: 587, SMTPTLSMode: "starttls", Username: "me@icloud.com", Password: "abcd-efgh-ijkl-mnop",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO users (id, username, username_normalized, name) VALUES ('attacker','attacker','attacker','A')`); err != nil {
		t.Fatal(err)
	}
	return &icloudFixture{t: t, h: h, db: db, dav: newFakeCalDAV(t), acct: acc.ID, day: time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 3)}
}

func (f *icloudFixture) stamp(offsetHours int) string {
	return f.day.Add(time.Duration(offsetHours) * time.Hour).Format("20060102T150405Z")
}

func (f *icloudFixture) ics(events ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Apple Inc.//iCloud//EN\r\n" + strings.Join(events, "") + "END:VCALENDAR\r\n"
}

func vev(lines ...string) string {
	return "BEGIN:VEVENT\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VEVENT\r\n"
}

func (f *icloudFixture) sync() error {
	return f.h.SyncCalendarAccount(context.Background(), f.acct)
}

func (f *icloudFixture) homeCal() models.Calendar {
	f.t.Helper()
	cals, _ := f.db.ListCalendarsForUser(context.Background(), "default")
	for _, c := range cals {
		if strings.HasSuffix(c.ProviderCalendarID, "/home/") {
			return c
		}
	}
	f.t.Fatalf("home calendar missing in %+v", cals)
	return models.Calendar{}
}

func (f *icloudFixture) events() []models.CalendarEvent {
	rows, err := f.db.Read().Query(`SELECT provider_event_id FROM calendar_events ORDER BY start_at, provider_event_id`)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	ctx := context.Background()
	var out []models.CalendarEvent
	for _, id := range ids {
		var eid int64
		_ = f.db.Read().QueryRow(`SELECT id FROM calendar_events WHERE provider_event_id = ?`, id).Scan(&eid)
		v, _, err := f.db.GetCalendarEventForUser(ctx, "default", eid)
		if err != nil {
			f.t.Fatal(err)
		}
		out = append(out, v.CalendarEvent)
	}
	return out
}

func (f *icloudFixture) ids() []string {
	var out []string
	for _, e := range f.events() {
		out = append(out, e.ProviderEventID)
	}
	return out
}

func (f *icloudFixture) localID(providerEventID string) string {
	f.t.Helper()
	var id int64
	if err := f.db.Read().QueryRow(`SELECT id FROM calendar_events WHERE provider_event_id = ?`, providerEventID).Scan(&id); err != nil {
		f.t.Fatalf("local id of %s: %v (have %v)", providerEventID, err, f.ids())
	}
	return strconv.FormatInt(id, 10)
}

func (f *icloudFixture) call(fn http.HandlerFunc, user, method string, form url.Values, id string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	fn(rec, calendarReq(user, method, "/x", form, id))
	return rec
}

func (f *icloudFixture) mustSync() {
	f.t.Helper()
	if err := f.sync(); err != nil {
		f.t.Fatal(err)
	}
}

const (
	hrefSolo   = fakeHomeCal + "solo.ics"
	hrefSeries = fakeHomeCal + "series.ics"
	hrefInvite = fakeHomeCal + "invite.ics"
)

func (f *icloudFixture) seed() {
	f.dav.set(hrefSolo, f.ics(vev("UID:solo", "DTSTAMP:20260101T000000Z", "SUMMARY:Solo", "DTSTART:"+f.stamp(15), "DTEND:"+f.stamp(16), "SEQUENCE:0")))
	f.dav.set(hrefSeries, f.ics(
		vev("UID:series", "DTSTAMP:20260101T000000Z", "SUMMARY:Standup", "DTSTART:"+f.stamp(17), "DTEND:"+f.stamp(18), "RRULE:FREQ=DAILY;COUNT=4", "SEQUENCE:2",
			"X-APPLE-CUSTOM:keep-me"),
		vev("UID:series", "DTSTAMP:20260101T000000Z", "SUMMARY:Standup moved", "RECURRENCE-ID:"+f.stamp(17+24), "DTSTART:"+f.stamp(19+24), "DTEND:"+f.stamp(20+24), "SEQUENCE:2")))
	f.dav.set(hrefInvite, f.ics(vev("UID:invite", "DTSTAMP:20260101T000000Z", "SUMMARY:Planning", "DTSTART:"+f.stamp(21), "DTEND:"+f.stamp(22), "SEQUENCE:1",
		"ORGANIZER;CN=Boss:mailto:boss@example.com", "ATTENDEE;PARTSTAT=ACCEPTED;CN=Boss:mailto:boss@example.com",
		"ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE;CN=Me:mailto:alias@icloud.com")))
}

func recKey(href, stamp string) string { return href + "#" + stamp }

// ---------- tests: discovery and calendar list ----------

func TestICloudDiscoveryAndCalendarMapping(t *testing.T) {
	f := newICloudFixture(t)
	f.mustSync()

	var order []string
	for _, r := range f.dav.requests("PROPFIND") {
		order = append(order, r.Path+"|"+r.Depth)
		if r.Auth != "me@icloud.com:abcd-efgh-ijkl-mnop" {
			t.Fatalf("basic auth = %q", r.Auth)
		}
	}
	if strings.Join(order, " ") != "/|0 /1/principal/|0 /1/calendars/|1" {
		t.Fatalf("discovery order = %v", order)
	}
	cals, err := f.db.ListCalendarsForUser(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]models.Calendar{}
	for _, c := range cals {
		got[c.Name] = c
	}
	if len(got) != 3 {
		t.Fatalf("calendars = %+v (the VTODO-only list must be skipped)", got)
	}
	home := got["Home"]
	if home.Provider != "icloud" || home.AccessRole != "owner" || !home.IsPrimary || home.Color != "#ff2968" || !home.Selected ||
		home.ProviderCalendarID != f.dav.srv.URL+fakeHomeCal {
		t.Fatalf("home = %+v", home)
	}
	if ro := got["Work (shared)"]; ro.AccessRole != "reader" || ro.IsPrimary || ro.Selected {
		t.Fatalf("read-only shared = %+v", ro)
	}
	if rw := got["Family"]; rw.AccessRole != "writer" || rw.Color != "#0a84ff" {
		t.Fatalf("writable shared = %+v", rw)
	}
	accounts, _ := f.db.ListCalendarAccounts(context.Background(), "default")
	providers := map[string]string{}
	for _, a := range accounts {
		providers[a.Email] = a.Provider
	}
	if providers["me@icloud.com"] != "icloud" || providers["user@example.com"] != "gmail" || len(providers) != 2 {
		t.Fatalf("calendar accounts = %+v", accounts)
	}
}

func TestICloudAccountDetection(t *testing.T) {
	ctx := context.Background()
	f := newICloudFixture(t)
	for i, c := range []struct {
		host, email, want string
	}{
		{"imap.example.com", "a@icloud.com", "icloud"}, {"imap.example.com", "a@ME.com", "icloud"}, {"imap.example.com", "a@mac.com", "icloud"},
		{"imap.mail.me.com", "a@example.org", "icloud"}, {"imap.example.com", "a@example.org", "imap"},
	} {
		id := fmt.Sprintf("det%d", i)
		if _, err := f.db.Write().ExecContext(ctx, `INSERT INTO accounts (id, user_id, provider, provider_account_id, email_address, imap_host) VALUES (?, 'default', 'imap', ?, ?, ?)`, id, id, c.email, c.host); err != nil {
			t.Fatal(err)
		}
		if got, _ := f.db.CalendarAccountProvider(ctx, id); got != c.want {
			t.Errorf("%s/%s = %q, want %q", c.host, c.email, got, c.want)
		}
	}
}

// ---------- tests: sync ----------

func TestICloudSyncCollectionInitialAndIncremental(t *testing.T) {
	f := newICloudFixture(t)
	f.seed()
	f.mustSync()

	// One-off, a 4-day series (day 2 replaced by an override), and the invite, all with stable ids.
	want := []string{
		hrefSolo,
		recKey(hrefSeries, f.stamp(17)),
		recKey(hrefSeries, f.stamp(17+24)),
		recKey(hrefSeries, f.stamp(17+48)),
		recKey(hrefSeries, f.stamp(17+72)),
		hrefInvite,
	}
	got := f.ids()
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("ids =\n%v\nwant\n%v", got, want)
	}
	evs := map[string]models.CalendarEvent{}
	for _, e := range f.events() {
		evs[e.ProviderEventID] = e
	}
	moved := evs[recKey(hrefSeries, f.stamp(17+24))]
	if moved.Title != "Standup moved" || moved.RecurringEventID != hrefSeries || moved.ICalUID != "series" ||
		moved.StartAt.Format("20060102T150405Z") != f.stamp(19+24) {
		t.Fatalf("override instance = %+v", moved)
	}
	if solo := evs[hrefSolo]; solo.RecurringEventID != "" || solo.Status != "confirmed" || solo.Title != "Solo" {
		t.Fatalf("solo = %+v", solo)
	}
	inv := evs[hrefInvite]
	if inv.SelfResponse != "needsAction" || inv.OrganizerEmail != "boss@example.com" || len(inv.Attendees) != 2 {
		t.Fatalf("invite = %+v", inv)
	}
	selfCount := 0
	for _, a := range inv.Attendees {
		if a.Self {
			selfCount++
			if a.Email != "alias@icloud.com" {
				t.Fatalf("self = %+v (alias comes from calendar-user-address-set)", a)
			}
		}
	}
	if selfCount != 1 {
		t.Fatalf("self attendees = %d", selfCount)
	}
	cal := f.homeCal()
	if cal.SyncToken == "" || cal.WindowStart == nil {
		t.Fatalf("token/window not stored: %+v", cal)
	}
	if n := len(f.dav.reportsOf("calendar-query")); n != 1 {
		t.Fatalf("initial calendar-query count = %d", n)
	}
	if !strings.Contains(f.dav.reportsOf("calendar-query")[0].Body, "<c:time-range start=") {
		t.Fatal("initial sync did not use a time-range")
	}
	if q := f.dav.reportsOf("calendar-query")[0].Body; strings.Contains(q, "expand") {
		t.Fatalf("expand must not be requested: %s", q)
	}

	// Incremental: one edited resource, one deleted resource (the series).
	f.dav.set(hrefSolo, f.ics(vev("UID:solo", "SUMMARY:Solo renamed", "DTSTART:"+f.stamp(15), "DTEND:"+f.stamp(16))))
	f.dav.remove(hrefSeries)
	f.dav.mu.Lock()
	f.dav.reqs = nil
	f.dav.mu.Unlock()
	f.mustSync()

	if n := len(f.dav.reportsOf("calendar-query")); n != 0 {
		t.Fatalf("incremental sync ran calendar-query %d times", n)
	}
	syncs := f.dav.reportsOf("sync-collection")
	if len(syncs) != 1 || !strings.Contains(syncs[0].Body, "<d:sync-token>"+cal.SyncToken+"</d:sync-token>") {
		t.Fatalf("sync-collection requests = %+v", syncs)
	}
	mg := f.dav.reportsOf("calendar-multiget")
	if len(mg) != 1 || !strings.Contains(mg[0].Body, hrefSolo) || strings.Contains(mg[0].Body, hrefSeries) || strings.Contains(mg[0].Body, hrefInvite) {
		t.Fatalf("multiget must list only the changed href: %+v", mg)
	}
	got = f.ids()
	sort.Strings(got)
	if strings.Join(got, " ") != hrefInvite+" "+hrefSolo {
		t.Fatalf("after deleting the series ids = %v", got)
	}
	if evs := f.events(); evs[0].Title != "Solo renamed" && evs[1].Title != "Solo renamed" {
		t.Fatalf("edit not applied: %+v", evs)
	}
	if newTok := f.homeCal().SyncToken; newTok == cal.SyncToken || newTok == "" {
		t.Fatalf("token not advanced: %q -> %q", cal.SyncToken, newTok)
	}
}

func TestICloudSyncFallsBackToWindowQueryWithoutSyncCollection(t *testing.T) {
	f := newICloudFixture(t)
	f.dav.noSync = true
	f.seed()
	f.mustSync()
	if tok := f.homeCal().SyncToken; tok != "" {
		t.Fatalf("token = %q without server support", tok)
	}
	if len(f.ids()) != 6 {
		t.Fatalf("ids = %v", f.ids())
	}
	f.dav.remove(hrefSeries)
	f.mustSync()
	if n := len(f.dav.reportsOf("calendar-query")); n != 2 {
		t.Fatalf("each sync must re-query the window, got %d queries", n)
	}
	if got := f.ids(); len(got) != 2 {
		t.Fatalf("series not removed by the replace: %v", got)
	}
}

func TestICloudRejectedSyncTokenResyncsWindow(t *testing.T) {
	f := newICloudFixture(t)
	f.seed()
	f.mustSync()
	f.dav.set(hrefSolo, f.ics(vev("UID:solo", "SUMMARY:After expiry", "DTSTART:"+f.stamp(15), "DTEND:"+f.stamp(16))))
	f.dav.expireToken = true
	f.mustSync()
	if n := len(f.dav.reportsOf("calendar-query")); n != 2 {
		t.Fatalf("expired token must trigger a full resync, queries = %d", n)
	}
	found := false
	for _, e := range f.events() {
		found = found || e.Title == "After expiry"
	}
	if !found {
		t.Fatal("resync did not pick up the change")
	}
}

func TestICloudMultigetParsing(t *testing.T) {
	f := newICloudFixture(t)
	f.seed()
	c := newICloudClient(f.dav.user, f.dav.pass)
	have, gone, err := c.multiget(context.Background(), f.dav.srv.URL+fakeHomeCal, []string{hrefSolo, fakeHomeCal + "gone.ics", hrefInvite})
	if err != nil {
		t.Fatal(err)
	}
	if len(have) != 2 || have[0].Key != hrefSolo || !strings.Contains(string(have[0].Data), "SUMMARY:Solo") || len(gone) != 1 || gone[0] != fakeHomeCal+"gone.ics" {
		t.Fatalf("have=%+v gone=%v", have, gone)
	}
}

func TestICloudUnauthorizedFlagsReconnectWithPasswordMessage(t *testing.T) {
	f := newICloudFixture(t)
	f.dav.unauth = true
	if err := f.sync(); !isICloudAuthError(err) {
		t.Fatalf("err = %v", err)
	}
	states, _ := f.db.ListCalendarAccountStates(context.Background(), "default")
	st := states[f.acct]
	if !st.NeedsReconnect || !strings.Contains(st.LastError, "app-specific password") || strings.Contains(strings.ToLower(st.LastError), "oauth") {
		t.Fatalf("state = %+v", st)
	}
	rec := httptest.NewRecorder()
	f.h.handleListCalendars(rec, calendarReq("default", http.MethodGet, "/api/calendar", nil, ""))
	if !strings.Contains(rec.Body.String(), `"provider":"icloud"`) || !strings.Contains(rec.Body.String(), `"needs_reconnect":true`) {
		t.Fatalf("accounts json = %s", rec.Body.String())
	}
	// The sync-all endpoint reports it the way the UI expects.
	rec = httptest.NewRecorder()
	f.h.handleSyncCalendars(rec, calendarReq("default", http.MethodPost, "/api/calendar/sync", nil, ""))
	if !strings.Contains(rec.Body.String(), "needs_reconnect") {
		t.Fatalf("sync response = %s", rec.Body.String())
	}
}

func TestICloudNeverSendsCredentialsOffICloud(t *testing.T) {
	var hits int
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer evil.Close()
	f := newICloudFixture(t)
	c := newICloudClient("u", "p")
	if _, err := c.do(context.Background(), "GET", evil.URL+"/x.ics", nil, nil); err == nil || hits != 0 {
		t.Fatalf("request to a foreign host: err=%v hits=%d", err, hits)
	}
	_ = f
}

// ---------- tests: writes ----------

func firstReq(t *testing.T, reqs []davRec) davRec {
	t.Helper()
	if len(reqs) == 0 {
		t.Fatal("no request recorded")
	}
	return reqs[0]
}

func TestICloudCreateWritesValidICS(t *testing.T) {
	f := newICloudFixture(t)
	f.mustSync()
	cal := f.homeCal()
	form := url.Values{
		"calendar_id": {strconv.FormatInt(cal.ID, 10)}, "title": {"Lunch, with; special \\ chars"}, "location": {"Bar"},
		"description": {"Line one\nLine two " + strings.Repeat("x", 120)},
		"start":       {"2026-10-07T09:00:00-07:00"}, "end": {"2026-10-07T10:30:00-07:00"}, "time_zone": {"America/Los_Angeles"},
		"attendees": {"pat@example.com"}, "add_meet": {"1"}, "reminder_minutes": {"10"},
	}
	rec := f.call(f.h.handleCreateCalendarEvent, "default", http.MethodPost, form, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"warning"`) || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	put := firstReq(t, f.dav.requests("PUT"))
	if put.IfNoneMatch != "*" || put.IfMatch != "" || !regexp.MustCompile(`^/1/calendars/home/[0-9a-f-]{36}\.ics$`).MatchString(put.Path) {
		t.Fatalf("PUT = %+v", put)
	}
	body := put.Body
	unfolded := strings.ReplaceAll(body, "\r\n ", "")
	for _, want := range []string{"BEGIN:VCALENDAR\r\nVERSION:2.0\r\n", "PRODID:", "BEGIN:VEVENT\r\n", "UID:", "DTSTAMP:", "LAST-MODIFIED:", "SEQUENCE:0\r\n",
		"DTSTART:20261007T160000Z\r\n", "DTEND:20261007T173000Z\r\n", `SUMMARY:Lunch\, with\; special \\ chars`, "LOCATION:Bar",
		"ORGANIZER;CN=\"me@icloud.com\":mailto:me@icloud.com", "ATTENDEE;PARTSTAT=NEEDS-ACTION;ROLE=REQ-PARTICIPANT;RSVP=TRUE;CN=\"pat@example.com\":mailto:pat@example.com",
		"BEGIN:VALARM\r\n", "TRIGGER:-PT10M\r\n", "END:VEVENT\r\nEND:VCALENDAR\r\n"} {
		if !strings.Contains(unfolded, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	for _, ln := range strings.Split(body, "\r\n") {
		if len(ln) > 75 {
			t.Errorf("unfolded line of %d octets: %q", len(ln), ln)
		}
	}
	if strings.Contains(strings.ReplaceAll(body, "\r\n", ""), "\n") {
		t.Error("bare LF in body")
	}
	if _, err := ical.ParseResource([]byte(body), time.UTC); err != nil {
		t.Fatalf("generated ics does not parse: %v", err)
	}
	// stored locally right away
	if ids := f.ids(); len(ids) != 1 || ids[0] != put.Path {
		t.Fatalf("local ids = %v", ids)
	}
}

func TestICloudCreateRecurringUsesTZIDAndRRule(t *testing.T) {
	f := newICloudFixture(t)
	f.mustSync()
	cal := f.homeCal()
	form := url.Values{
		"calendar_id": {strconv.FormatInt(cal.ID, 10)}, "title": {"Weekly"}, "recurrence": {"weekly"},
		"start": {"2026-10-07T09:00:00-07:00"}, "end": {"2026-10-07T10:00:00-07:00"}, "time_zone": {"America/Los_Angeles"},
	}
	if rec := f.call(f.h.handleCreateCalendarEvent, "default", http.MethodPost, form, ""); rec.Code != 200 {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	body := firstReq(t, f.dav.requests("PUT")).Body
	for _, want := range []string{"DTSTART;TZID=America/Los_Angeles:20261007T090000\r\n", "DTEND;TZID=America/Los_Angeles:20261007T100000\r\n", "RRULE:FREQ=WEEKLY\r\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "ATTENDEE") || strings.Contains(body, "ORGANIZER") {
		t.Error("no guests, so no scheduling lines")
	}
	// all-day uses DATE values
	form = url.Values{"calendar_id": {strconv.FormatInt(cal.ID, 10)}, "title": {"Holiday"}, "all_day": {"1"}, "start_date": {"2026-12-24"}, "end_date": {"2026-12-26"}}
	if rec := f.call(f.h.handleCreateCalendarEvent, "default", http.MethodPost, form, ""); rec.Code != 200 {
		t.Fatalf("all-day create = %d %s", rec.Code, rec.Body.String())
	}
	puts := f.dav.requests("PUT")
	if b := puts[len(puts)-1].Body; !strings.Contains(b, "DTSTART;VALUE=DATE:20261224\r\n") || !strings.Contains(b, "DTEND;VALUE=DATE:20261226\r\n") {
		t.Fatalf("all-day body:\n%s", b)
	}
}

func TestICloudEditOccurrenceAddsOverrideAndSeriesEditsMaster(t *testing.T) {
	f := newICloudFixture(t)
	f.seed()
	f.mustSync()
	etag := f.dav.res[hrefSeries].etag

	// Edit the third occurrence only.
	third := f.localID(recKey(hrefSeries, f.stamp(17+48)))
	form := url.Values{"title": {"Third only"}, "scope": {"this"}}
	rec := f.call(f.h.handlePatchCalendarEvent, "default", http.MethodPatch, form, third)
	if rec.Code != 200 {
		t.Fatalf("patch this = %d %s", rec.Code, rec.Body.String())
	}
	put := firstReq(t, f.dav.requests("PUT"))
	if put.Path != hrefSeries || put.IfMatch != etag || put.IfNoneMatch != "" {
		t.Fatalf("PUT = %+v (want If-Match %s)", put, etag)
	}
	res, err := ical.ParseResource([]byte(put.Body), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 3 {
		t.Fatalf("want master + 2 overrides, got %d events:\n%s", len(res.Events), put.Body)
	}
	recID, _ := time.Parse("20060102T150405Z", f.stamp(17+48))
	ov := res.Override(recID)
	if ov == nil || ov.Summary != "Third only" || ov.RRule != "" || len(ov.ExDates) != 0 || ov.Sequence != 3 {
		t.Fatalf("override = %+v", ov)
	}
	if ov.Start.Format("20060102T150405Z") != f.stamp(17+48) {
		t.Fatalf("override start = %v", ov.Start)
	}
	if !strings.Contains(put.Body, "X-APPLE-CUSTOM:keep-me") || !strings.Contains(put.Body, "RECURRENCE-ID:"+f.stamp(17+24)) {
		t.Fatalf("existing lines were lost:\n%s", put.Body)
	}
	if m := res.Master(); m.Summary != "Standup" || m.Sequence != 2 {
		t.Fatalf("master touched by an occurrence edit: %+v", m)
	}
	if title := f.eventTitle(recKey(hrefSeries, f.stamp(17+48))); title != "Third only" {
		t.Fatalf("local title = %q", title)
	}

	// Edit the series from an occurrence: the master changes, shifted by the same delta.
	first := f.localID(recKey(hrefSeries, f.stamp(17)))
	form = url.Values{"title": {"Whole series"}, "scope": {"series"}, "start": {"2026-10-01T10:00:00Z"}, "end": {"2026-10-01T11:30:00Z"}, "time_zone": {"UTC"}}
	// the occurrence is 17:00-18:00 UTC; moving it to 10:00-11:30 shifts the master by -7h and lengthens it to 1.5h
	start := f.day.Add(10 * time.Hour)
	form.Set("start", start.Format(time.RFC3339))
	form.Set("end", start.Add(90*time.Minute).Format(time.RFC3339))
	f.dav.mu.Lock()
	f.dav.reqs = nil
	f.dav.mu.Unlock()
	rec = f.call(f.h.handlePatchCalendarEvent, "default", http.MethodPatch, form, first)
	if rec.Code != 200 {
		t.Fatalf("patch series = %d %s", rec.Code, rec.Body.String())
	}
	res, _ = ical.ParseResource([]byte(firstReq(t, f.dav.requests("PUT")).Body), time.UTC)
	m := res.Master()
	if m.Summary != "Whole series" || m.Start.Format("20060102T150405Z") != f.stamp(10) || m.End.Sub(m.Start) != 90*time.Minute || m.Sequence != 3 {
		t.Fatalf("master = %+v", m)
	}
	if !strings.Contains(m.Comp.Get("DTSTAMP").Value, "Z") || m.Comp.Get("LAST-MODIFIED") == nil {
		t.Fatal("DTSTAMP and LAST-MODIFIED must be set on edits")
	}
}

func (f *icloudFixture) eventTitle(id string) string {
	for _, e := range f.events() {
		if e.ProviderEventID == id {
			return e.Title
		}
	}
	return ""
}

func TestICloudDeleteOccurrenceAddsExdateAndSeriesDeletes(t *testing.T) {
	f := newICloudFixture(t)
	f.seed()
	f.mustSync()
	etag := f.dav.res[hrefSeries].etag

	second := f.localID(recKey(hrefSeries, f.stamp(17+24))) // the overridden occurrence
	rec := f.call(f.h.handleDeleteCalendarEvent, "default", http.MethodDelete, nil, second)
	if rec.Code != 200 {
		t.Fatalf("delete this = %d %s", rec.Code, rec.Body.String())
	}
	put := firstReq(t, f.dav.requests("PUT"))
	if put.IfMatch != etag || !strings.Contains(put.Body, "EXDATE:"+f.stamp(17+24)) {
		t.Fatalf("PUT = %+v", put)
	}
	res, _ := ical.ParseResource([]byte(put.Body), time.UTC)
	if len(res.Events) != 1 {
		t.Fatalf("the override of a deleted occurrence must go too: %d events", len(res.Events))
	}
	for _, id := range f.ids() {
		if id == recKey(hrefSeries, f.stamp(17+24)) {
			t.Fatal("deleted occurrence still stored")
		}
	}
	if len(f.dav.requests("DELETE")) != 0 {
		t.Fatal("deleting one occurrence must not DELETE the resource")
	}

	etag = f.dav.res[hrefSeries].etag
	third := f.localID(recKey(hrefSeries, f.stamp(17+48)))
	rec = httptest.NewRecorder()
	f.h.handleDeleteCalendarEvent(rec, calendarReq("default", http.MethodDelete, "/x?scope=series", nil, third))
	if rec.Code != 200 {
		t.Fatalf("delete series = %d %s", rec.Code, rec.Body.String())
	}
	del := firstReq(t, f.dav.requests("DELETE"))
	if del.Path != hrefSeries || del.IfMatch != etag {
		t.Fatalf("DELETE = %+v, want If-Match %s", del, etag)
	}
	for _, id := range f.ids() {
		if strings.HasPrefix(id, hrefSeries) {
			t.Fatalf("series instances left: %v", f.ids())
		}
	}
	// a plain event deletes its resource
	solo := f.localID(hrefSolo)
	if rec := f.call(f.h.handleDeleteCalendarEvent, "default", http.MethodDelete, nil, solo); rec.Code != 200 || f.dav.get(hrefSolo) != "" {
		t.Fatalf("delete solo = %d, resource still there: %v", rec.Code, f.dav.get(hrefSolo) != "")
	}
}

func TestICloudRSVPChangesOnlyPartstat(t *testing.T) {
	f := newICloudFixture(t)
	f.seed()
	f.mustSync()
	before := f.dav.get(hrefInvite)
	etag := f.dav.res[hrefInvite].etag
	rec := f.call(f.h.handleRSVPCalendarEvent, "default", http.MethodPost, url.Values{"response": {"accepted"}}, f.localID(hrefInvite))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"response":"accepted"`) {
		t.Fatalf("rsvp = %d %s", rec.Code, rec.Body.String())
	}
	put := firstReq(t, f.dav.requests("PUT"))
	if put.IfMatch != etag {
		t.Fatalf("PUT = %+v", put)
	}
	after := put.Body
	// Exactly one line differs: our ATTENDEE's PARTSTAT. SEQUENCE/DTSTAMP/SUMMARY are untouched (RFC 6638 3.2.2.3).
	norm := func(s string) []string { return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") }
	var diffs []string
	b, a := norm(before), norm(after)
	if len(b) != len(a) {
		t.Fatalf("line count changed %d -> %d\n%s", len(b), len(a), after)
	}
	for i := range b {
		if b[i] != a[i] {
			diffs = append(diffs, b[i]+" => "+a[i])
		}
	}
	if len(diffs) != 1 || !strings.Contains(diffs[0], "PARTSTAT=NEEDS-ACTION") || !strings.Contains(diffs[0], "PARTSTAT=ACCEPTED") || !strings.Contains(diffs[0], "mailto:alias@icloud.com") {
		t.Fatalf("diff = %v", diffs)
	}
	for _, e := range f.events() {
		if e.ProviderEventID == hrefInvite && e.SelfResponse != "accepted" {
			t.Fatalf("local self response = %q", e.SelfResponse)
		}
	}
	// declined / tentative map too
	for resp, want := range map[string]string{"declined": "PARTSTAT=DECLINED", "tentative": "PARTSTAT=TENTATIVE"} {
		f.call(f.h.handleRSVPCalendarEvent, "default", http.MethodPost, url.Values{"response": {resp}}, f.localID(hrefInvite))
		puts := f.dav.requests("PUT")
		if !strings.Contains(puts[len(puts)-1].Body, want) {
			t.Fatalf("%s: %s", resp, puts[len(puts)-1].Body)
		}
	}
}

func TestICloudRSVPToOneOccurrenceWritesAnOverride(t *testing.T) {
	f := newICloudFixture(t)
	href := fakeHomeCal + "weekly-invite.ics"
	f.dav.set(href, f.ics(vev("UID:wi", "DTSTAMP:20260101T000000Z", "SUMMARY:Sync", "DTSTART:"+f.stamp(9), "DTEND:"+f.stamp(10), "RRULE:FREQ=DAILY;COUNT=3", "SEQUENCE:4",
		"ORGANIZER:mailto:boss@example.com", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:boss@example.com", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:me@icloud.com")))
	f.mustSync()
	second := f.localID(recKey(href, f.stamp(9+24)))
	rec := f.call(f.h.handleRSVPCalendarEvent, "default", http.MethodPost, url.Values{"response": {"declined"}, "scope": {"this"}}, second)
	if rec.Code != 200 {
		t.Fatalf("rsvp = %d %s", rec.Code, rec.Body.String())
	}
	res, err := ical.ParseResource([]byte(firstReq(t, f.dav.requests("PUT")).Body), time.UTC)
	if err != nil || len(res.Events) != 2 {
		t.Fatalf("parse: %v events=%d", err, len(res.Events))
	}
	if m := res.Master(); m.Attendees[1].PartStat != "NEEDS-ACTION" || m.Sequence != 4 {
		t.Fatalf("master changed: %+v", m)
	}
	rid, _ := time.Parse("20060102T150405Z", f.stamp(9+24))
	ov := res.Override(rid)
	if ov == nil || ov.Attendees[1].PartStat != "DECLINED" || ov.Sequence != 4 || ov.RRule != "" || ov.Start.Format("20060102T150405Z") != f.stamp(9+24) {
		t.Fatalf("override = %+v", ov)
	}
}

func TestICloudRSVPRejectsNonGuest(t *testing.T) {
	f := newICloudFixture(t)
	f.seed()
	f.mustSync()
	rec := f.call(f.h.handleRSVPCalendarEvent, "default", http.MethodPost, url.Values{"response": {"accepted"}}, f.localID(hrefSolo))
	if rec.Code != http.StatusBadRequest || len(f.dav.requests("PUT")) != 0 {
		t.Fatalf("rsvp on own event = %d, puts=%d", rec.Code, len(f.dav.requests("PUT")))
	}
}

func TestICloud412IsAClearConflictAndRefreshesLocalCopy(t *testing.T) {
	f := newICloudFixture(t)
	f.seed()
	f.mustSync()
	f.dav.flipPath = hrefSolo // someone else edits the resource between our GET and PUT
	rec := f.call(f.h.handlePatchCalendarEvent, "default", http.MethodPatch, url.Values{"title": {"Mine"}}, f.localID(hrefSolo))
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" || !strings.Contains(rec.Body.String(), "changed elsewhere") {
		t.Fatalf("patch on 412 = %d %s", rec.Code, rec.Body.String())
	}
	if put := firstReq(t, f.dav.requests("PUT")); put.IfMatch == "" {
		t.Fatalf("PUT must be conditional: %+v", put)
	}
	if got := f.eventTitle(hrefSolo); got != "Elsewhere" {
		t.Fatalf("local copy not refreshed after 412: %q", got)
	}
}

func TestICloudWritesRejectForeignUserWithoutDAVCall(t *testing.T) {
	f := newICloudFixture(t)
	f.seed()
	f.mustSync()
	cal := f.homeCal()
	idStr := f.localID(hrefInvite)
	calls := f.dav.total()
	for name, run := range map[string]func(*httptest.ResponseRecorder){
		"create": func(rec *httptest.ResponseRecorder) {
			f.h.handleCreateCalendarEvent(rec, calendarReq("attacker", http.MethodPost, "/x", url.Values{"calendar_id": {strconv.FormatInt(cal.ID, 10)}, "title": {"x"}, "start": {"2026-10-07T09:00:00Z"}, "end": {"2026-10-07T10:00:00Z"}}, ""))
		},
		"patch": func(rec *httptest.ResponseRecorder) {
			f.h.handlePatchCalendarEvent(rec, calendarReq("attacker", http.MethodPatch, "/x", url.Values{"title": {"x"}}, idStr))
		},
		"delete": func(rec *httptest.ResponseRecorder) {
			f.h.handleDeleteCalendarEvent(rec, calendarReq("attacker", http.MethodDelete, "/x", nil, idStr))
		},
		"rsvp": func(rec *httptest.ResponseRecorder) {
			f.h.handleRSVPCalendarEvent(rec, calendarReq("attacker", http.MethodPost, "/x", url.Values{"response": {"accepted"}}, idStr))
		},
	} {
		rec := httptest.NewRecorder()
		run(rec)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s as a foreign user = %d, want 404", name, rec.Code)
		}
	}
	if f.dav.total() != calls {
		t.Fatalf("foreign requests reached CalDAV %d times", f.dav.total()-calls)
	}
}

func TestICloudReadOnlyCalendarWriteIs403WithoutDAVCall(t *testing.T) {
	f := newICloudFixture(t)
	f.mustSync()
	var ro models.Calendar
	cals, _ := f.db.ListCalendarsForUser(context.Background(), "default")
	for _, c := range cals {
		if c.AccessRole == "reader" {
			ro = c
		}
	}
	calls := f.dav.total()
	rec := f.call(f.h.handleCreateCalendarEvent, "default", http.MethodPost, url.Values{"calendar_id": {strconv.FormatInt(ro.ID, 10)}, "title": {"x"},
		"start": {"2026-10-07T09:00:00Z"}, "end": {"2026-10-07T10:00:00Z"}}, "")
	if rec.Code != http.StatusForbidden || f.dav.total() != calls {
		t.Fatalf("read-only create = %d, extra calls %d", rec.Code, f.dav.total()-calls)
	}
}

func TestICloudWrite401FlagsReconnect(t *testing.T) {
	f := newICloudFixture(t)
	f.seed()
	f.mustSync()
	f.dav.unauth = true
	rec := f.call(f.h.handlePatchCalendarEvent, "default", http.MethodPatch, url.Values{"title": {"x"}}, f.localID(hrefSolo))
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "needs_reconnect" || !strings.Contains(rec.Body.String(), "app-specific password") {
		t.Fatalf("patch 401 = %d %s", rec.Code, rec.Body.String())
	}
	states, _ := f.db.ListCalendarAccountStates(context.Background(), "default")
	if !states[f.acct].NeedsReconnect {
		t.Fatal("reconnect flag not set")
	}
}
