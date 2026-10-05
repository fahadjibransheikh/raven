package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func inviteMIME(method, vevent string) string {
	ics := "BEGIN:VCALENDAR\nVERSION:2.0\n" + method + "BEGIN:VEVENT\n" + vevent + "END:VEVENT\nEND:VCALENDAR\n"
	msg := "From: boss@example.com\nTo: user@example.com\nSubject: Invitation\nMIME-Version: 1.0\n" +
		"Content-Type: multipart/alternative; boundary=\"alt\"\n\n--alt\nContent-Type: text/plain\n\nhi\n" +
		"--alt\nContent-Type: text/calendar; charset=UTF-8\n\n" + ics + "--alt--\n"
	return strings.ReplaceAll(msg, "\n", "\r\n")
}

// insertRawMessage stores a message in account "acc" whose raw MIME is on disk.
func insertRawMessage(t *testing.T, f *calWriteFixture, id int64, raw string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "raw.eml")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.db.Write().Exec(`INSERT INTO messages (id, account_id, internet_message_id, subject, from_email, raw_path)
		VALUES (?, 'acc', ?, 'Invitation', 'boss@example.com', ?)`, id, "<m"+strconv.FormatInt(id, 10)+"@x>", path); err != nil {
		t.Fatal(err)
	}
}

func (f *calWriteFixture) inviteCard(user string, id int64) *httptest.ResponseRecorder {
	req := calendarReq(user, http.MethodGet, "/email/x/invite", nil, strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	f.h.handleEmailInvite(rec, req)
	return rec
}

func TestEmailInviteCardFoundVersusNotFound(t *testing.T) {
	f := newCalWriteFixture(t)
	if _, err := f.h.db.Write().Exec(`UPDATE calendar_events SET ical_uid = 'uid-inv@google.com', self_response = 'tentative' WHERE provider_event_id = 'inv'`); err != nil {
		t.Fatal(err)
	}
	wantID := f.localID("inv")
	other := f.localID("solo")

	vevent := "UID:%s\nSUMMARY:Planning\nDTSTART:20261007T160000Z\nDTEND:20261007T170000Z\nORGANIZER;CN=Boss:mailto:boss@example.com\nX-GOOGLE-CONFERENCE:https://meet.google.com/abc-defg-hij\nLOCATION:Room 1\n"
	insertRawMessage(t, f, 1, inviteMIME("METHOD:REQUEST\n", strings.Replace(vevent, "%s", "uid-inv@google.com", 1)))
	insertRawMessage(t, f, 2, inviteMIME("METHOD:REQUEST\n", strings.Replace(vevent, "%s", "uid-unknown@google.com", 1)))

	rec := f.inviteCard("default", 1)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{
		`data-event-id="` + wantID + `"`, `data-invite-rsvp="accepted"`, `data-invite-rsvp="tentative"`, `data-invite-rsvp="declined"`,
		`Planning`, `Maybe`, `https://meet.google.com/abc-defg-hij`, `Boss (boss@example.com)`, `Room 1`,
		`Oct 7, 2026, 4:00 PM`, `href="/calendar?date=2026-10-07"`, `data-start="2026-10-07T16:00:00Z"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("found card missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, `data-event-id="`+other+`"`) || strings.Contains(body, "data-invite-sync") {
		t.Errorf("found card picked the wrong event or shows sync:\n%s", body)
	}

	body = f.inviteCard("default", 2).Body.String()
	if !strings.Contains(body, "Not in your calendar yet") || !strings.Contains(body, "data-invite-sync") || strings.Contains(body, "data-invite-rsvp") || strings.Contains(body, `data-event-id="`+wantID+`"`) {
		t.Errorf("not-found card wrong:\n%s", body)
	}
}

func TestEmailInviteCardEscapesUntrustedFields(t *testing.T) {
	f := newCalWriteFixture(t)
	vevent := "UID:evil\nSUMMARY:<script>alert(1)</script>\nDTSTART:20261007T160000Z\n" +
		"LOCATION:javascript:alert(2)\nORGANIZER;CN=\"<img src=x onerror=alert(3)>\":mailto:x@example.com\n" +
		"DESCRIPTION:join javascript:alert(4) or https://zoom.us/j/1\" onclick=\"alert(5)\n"
	insertRawMessage(t, f, 1, inviteMIME("METHOD:REQUEST\n", vevent))
	body := f.inviteCard("default", 1).Body.String()
	for _, bad := range []string{"<script>alert(1)", "<img src=x", `href="javascript:`, `onclick="alert(5)`} {
		if strings.Contains(body, bad) {
			t.Errorf("unescaped %q in\n%s", bad, body)
		}
	}
	for _, want := range []string{"&lt;script&gt;alert(1)&lt;/script&gt;", "&lt;img src=x onerror=alert(3)&gt;", "javascript:alert(2)"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing escaped %q in\n%s", want, body)
		}
	}
	if !strings.Contains(body, `href="https://zoom.us/j/1"`) {
		t.Errorf("zoom link should survive, got\n%s", body)
	}
}

func TestEmailInviteCancelReplyAndNone(t *testing.T) {
	f := newCalWriteFixture(t)
	insertRawMessage(t, f, 1, inviteMIME("METHOD:CANCEL\n", "UID:uid-inv\nSUMMARY:Gone\nSTATUS:CANCELLED\nDTSTART:20261007T160000Z\n"))
	body := f.inviteCard("default", 1).Body.String()
	if !strings.Contains(body, "This event was cancelled") || strings.Contains(body, "data-invite-rsvp") || strings.Contains(body, "data-invite-sync") {
		t.Errorf("cancel card:\n%s", body)
	}
	insertRawMessage(t, f, 2, inviteMIME("METHOD:REPLY\n", "UID:uid-inv\nSUMMARY:Lunch\nDTSTART:20261007T160000Z\nATTENDEE;PARTSTAT=ACCEPTED;CN=Bob:mailto:bob@example.com\n"))
	body = f.inviteCard("default", 2).Body.String()
	if !strings.Contains(body, "Bob accepted") || strings.Contains(body, "data-invite-rsvp") {
		t.Errorf("reply card:\n%s", body)
	}
	insertRawMessage(t, f, 3, "From: a@example.com\r\nSubject: hi\r\nContent-Type: text/plain\r\n\r\nno invite\r\n")
	if rec := f.inviteCard("default", 3); rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Errorf("plain mail: %d %q", rec.Code, rec.Body)
	}
	insertRawMessage(t, f, 4, inviteMIME("METHOD:COUNTER\n", "UID:x\nSUMMARY:Counter\nDTSTART:20261007T160000Z\n"))
	if rec := f.inviteCard("default", 4); rec.Code != http.StatusNoContent {
		t.Errorf("counter: %d", rec.Code)
	}
}

func TestEmailInviteOwnership(t *testing.T) {
	f := newCalWriteFixture(t)
	insertRawMessage(t, f, 1, inviteMIME("METHOD:REQUEST\n", "UID:uid-inv\nSUMMARY:Secret\nDTSTART:20261007T160000Z\n"))
	if rec := f.inviteCard("attacker", 1); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "Secret") {
		t.Fatalf("foreign user: %d %s", rec.Code, rec.Body)
	}
	if rec := f.inviteCard("default", 999); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", rec.Code)
	}
}
