package message

import (
	"context"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/store"
)

const icsBody = "BEGIN:VCALENDAR\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:u1\r\nSUMMARY:Sync\r\nDTSTART:20261007T160000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func crlfMsg(s string) string { return strings.ReplaceAll(strings.TrimLeft(s, "\n"), "\n", "\r\n") }

// Google-style: multipart/alternative with a text/calendar sibling, plus an invite.ics attachment.
var googleStyle = crlfMsg(`
From: boss@example.com
To: me@example.com
Subject: Invitation: Sync
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="mix"

--mix
Content-Type: multipart/alternative; boundary="alt"

--alt
Content-Type: text/plain; charset="UTF-8"

You have been invited
--alt
Content-Type: text/html; charset="UTF-8"

<p>You have been invited</p>
--alt
Content-Type: text/calendar; charset="UTF-8"; method=REQUEST

` + icsBody + `--alt--
--mix
Content-Type: application/ics; name="invite.ics"
Content-Disposition: attachment; filename="invite.ics"

` + icsBody + `--mix--
`)

func TestExtractCalendarFromAlternativePart(t *testing.T) {
	got := ExtractCalendar(strings.NewReader(googleStyle))
	if !strings.Contains(string(got), "UID:u1") {
		t.Fatalf("got %q", got)
	}
}

func TestExtractCalendarFromIcsAttachmentAndBase64(t *testing.T) {
	// Outlook-style: octet-stream attachment identified by its file name, base64 encoded.
	msg := crlfMsg(`
From: a@example.com
Subject: x
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="b"

--b
Content-Type: text/plain

hi
--b
Content-Type: application/octet-stream; name="meeting.ics"
Content-Disposition: attachment; filename="meeting.ics"
Content-Transfer-Encoding: base64

QkVHSU46VkNBTEVOREFSDQpCRUdJTjpWRVZFTlQNClVJRDpiNjQNCkVORDpWRVZFTlQNCkVORDpWQ0FMRU5EQVINCg==
--b--
`)
	if got := ExtractCalendar(strings.NewReader(msg)); !strings.Contains(string(got), "UID:b64") {
		t.Fatalf("got %q", got)
	}
}

func TestExtractCalendarNoneAndGarbage(t *testing.T) {
	plain := "From: a@example.com\r\nSubject: x\r\nContent-Type: text/plain\r\n\r\nhello\r\n"
	for _, in := range []string{plain, "", "not a message at all", "Content-Type: multipart/mixed; boundary=\"zz\"\r\n\r\n--zz\r\n"} {
		if got := ExtractCalendar(strings.NewReader(in)); got != nil {
			t.Errorf("%q: got %q, want nil", in, got)
		}
	}
}

func TestParseMessageKeepsPlainBodyWhenInviteIsAlternative(t *testing.T) {
	bs := store.NewBlobStore(t.TempDir())
	parsed, err := ParseMessage(context.Background(), strings.NewReader(googleStyle), bs, "acc", 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(parsed.TextBody, "BEGIN:VCALENDAR") || !strings.Contains(parsed.TextBody, "You have been invited") {
		t.Fatalf("TextBody = %q: the text/calendar part must not replace the body", parsed.TextBody)
	}
}
