package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/auth"
	"github.com/fahadjibransheikh/raven/internal/calendar"
	"github.com/fahadjibransheikh/raven/internal/storage"
	"github.com/fahadjibransheikh/raven/internal/store"
)

const mailInvitationICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:invite-uid@example.com\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:20261020T130000Z\r\nDTEND:20261020T140000Z\r\nSUMMARY:Planning\r\nORGANIZER;CN=Host:mailto:host@example.com\r\nATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:one@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

// inviteLookupFixture is an account whose calendar has not cached the event the
// email invites it to; the provider fake returns it only when asked for a
// window around the start.
func inviteLookupFixture(t *testing.T) (*Handler, *[]time.Time) {
	t.Helper()
	h := calendarResponseFixture(t, false)
	if _, err := h.db.Write().Exec(`INSERT INTO folders(id,account_id,name,role,remote_id) VALUES('incoming','one-account','Inbox','inbox','INBOX')`); err != nil {
		t.Fatal(err)
	}
	h.blobStore = store.NewBlobStore(filepath.Join(t.TempDir(), "blobs"))
	// Fixture setup counted as a sync; the periodic sync last ran long ago.
	if _, err := h.db.Write().Exec(`UPDATE calendar_sync_state SET last_success_at = datetime('now', '-1 hour')`); err != nil {
		t.Fatal(err)
	}
	var windows []time.Time
	start := time.Date(2026, 10, 20, 13, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	h.calendarFetchEvents = func(_ context.Context, source storage.CalendarSource, query calendar.EventQuery) (calendar.EventPage, error) {
		windows = append(windows, query.WindowStart)
		if source.ID != "one-source" || query.WindowStart.After(start) || query.WindowEnd.Before(end) {
			return calendar.EventPage{}, nil
		}
		return calendar.EventPage{Events: []calendar.RemoteEvent{{RemoteID: "remote-invite", ICalUID: "invite-uid@example.com", ETag: `"v1"`, Summary: "Planning",
			OrganizerEmail: "host@example.com", ResponseStatus: "needsAction", StartAt: &start, EndAt: &end,
			Attendees: json.RawMessage(`[{"email":"one@example.com","self":true,"responseStatus":"needsAction"}]`)}}}, nil
	}
	return h, &windows
}

func storeInviteMail(t *testing.T, h *Handler, ics string) string {
	t.Helper()
	raw := []byte("From: Host <host@example.com>\r\nTo: one@example.com\r\nSubject: Invitation: Planning\r\nMIME-Version: 1.0\r\nContent-Type: text/calendar; method=REQUEST; charset=utf-8\r\n\r\n" + ics)
	result, err := h.db.Write().Exec(`INSERT INTO messages(account_id,from_email) VALUES('one-account','host@example.com')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	path, err := h.blobStore.StoreRaw(t.Context(), "one-account", id, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Write().Exec(`UPDATE messages SET raw_path=? WHERE id=?`, path, id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Write().Exec(`INSERT INTO message_folder_state(message_id,folder_id,remote_uid) VALUES(?,'incoming',?)`, id, id); err != nil {
		t.Fatal(err)
	}
	return strconv.FormatInt(id, 10)
}

func mailFooterBody(t *testing.T, h *Handler, mailID string) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/mail/"+mailID+"/calendar", nil)
	r.SetPathValue("id", mailID)
	w := httptest.NewRecorder()
	h.handleMailCalendarFooter(w, r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{ID: "one"})))
	return w.Body.String()
}

func TestMailInvitationNotCachedIsLookedUpAndAnswerable(t *testing.T) {
	h, windows := inviteLookupFixture(t)
	mailID := storeInviteMail(t, h, mailInvitationICS)
	body := mailFooterBody(t, h, mailID)
	if len(*windows) != 1 {
		t.Fatalf("provider lookups = %d, want 1", len(*windows))
	}
	if !strings.Contains(body, "data-calendar-response-form") || !strings.Contains(body, "Accept") || strings.Contains(body, "could not be matched") {
		t.Fatalf("invitation not answerable in the email: %s", body)
	}
	cached, err := h.db.ListMailCalendarEvents(t.Context(), "one", "one-account", "invite-uid@example.com")
	if err != nil || len(cached) != 1 || cached[0].ResponseStatus != "needsAction" {
		t.Fatalf("looked-up event not cached: %v %+v", err, cached)
	}
	// The cached event now answers the existing RSVP path, through the provider fake.
	sent := 0
	h.calendarSendResponse = func(_ context.Context, source storage.CalendarSource, event storage.CalendarEvent, target calendarResponseTarget, response string) (calendarResponseResult, error) {
		sent++
		if source.ID != "one-source" || event.ICalUID != "invite-uid@example.com" || response != "accepted" {
			t.Fatal("wrong response target")
		}
		target.Event.ETag, target.Event.ResponseStatus = `"v2"`, response
		return calendarResponseResult{Event: target.Event}, nil
	}
	h.calendarReadResponse = func(_ context.Context, _ storage.CalendarSource, cached storage.CalendarEvent, scope string) (calendarResponseTarget, error) {
		return calendarResponseTarget{Scope: scope, Event: calendar.RemoteEvent{RemoteID: cached.RemoteID, ICalUID: cached.ICalUID, ETag: cached.ETag, ResponseStatus: "needsAction"}}, nil
	}
	r := calendarResponseRequest("event", `"v1"`, "accepted", "one")
	r.SetPathValue("id", cached[0].ID)
	w := httptest.NewRecorder()
	h.handleCalendarResponse(w, r)
	if w.Code != 200 || sent != 1 || !strings.Contains(w.Body.String(), `"responded":true`) {
		t.Fatalf("status=%d sent=%d body=%s", w.Code, sent, w.Body.String())
	}
}

func TestMailInvitationLookupSkipsFreshSyncAndOtherMail(t *testing.T) {
	h, windows := inviteLookupFixture(t)
	other := strings.Replace(mailInvitationICS, "invite-uid@example.com", "elsewhere@example.com", 1)
	other = strings.Replace(other, "20261020T130000Z", "20271020T130000Z", 1)
	other = strings.Replace(other, "20261020T140000Z", "20271020T140000Z", 1)
	mailID := storeInviteMail(t, h, other)
	first := mailFooterBody(t, h, mailID)
	if len(*windows) != 1 || !strings.Contains(first, "could not be matched") {
		t.Fatalf("unmatched invitation: lookups=%d body=%s", len(*windows), first)
	}
	// The source just synced, so reopening must not ask the provider again.
	mailFooterBody(t, h, mailID)
	if len(*windows) != 1 {
		t.Fatalf("provider asked again within the cooldown: %d", len(*windows))
	}
}

func TestMailInvitationWithoutSelectedCalendarSaysSo(t *testing.T) {
	h, windows := inviteLookupFixture(t)
	if _, err := h.db.Write().Exec(`UPDATE calendar_sources SET is_selected = 0 WHERE account_id = 'one-account'`); err != nil {
		t.Fatal(err)
	}
	body := mailFooterBody(t, h, storeInviteMail(t, h, mailInvitationICS))
	if len(*windows) != 0 || !strings.Contains(body, "No calendar is selected") {
		t.Fatalf("lookups=%d body=%s", len(*windows), body)
	}
}

func TestMovedOccurrenceMatchesByOriginalStart(t *testing.T) {
	original := time.Date(2026, 10, 13, 13, 30, 0, 0, time.UTC)
	moved := original.Add(30 * time.Minute)
	next := original.AddDate(0, 0, 14)
	events := []storage.CalendarEvent{
		{ID: "moved", RemoteID: "series_20261013T133000Z", StartAt: &moved, OrganizerEmail: "adam@codup.co"},
		{ID: "later", RemoteID: "series_20261027T133000Z", StartAt: &next, OrganizerEmail: "adam@codup.co"},
	}
	item := mailCalendarEvent{Organizer: "adam@codup.co", Recurrence: &original, HasRecurrence: true}
	if got, ok := matchMailCalendarEvents(events, item); !ok || got.ID != "moved" {
		t.Fatalf("moved occurrence not matched: %v %v", got.ID, ok)
	}
	elsewhere := original.AddDate(0, 0, 1)
	item.Recurrence = &elsewhere
	if _, ok := matchMailCalendarEvents(events, item); ok {
		t.Fatal("unrelated recurrence matched")
	}
}
