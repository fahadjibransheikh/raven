package message

import (
	"context"
	"strings"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/store"
)

func TestInlinePartsAreKeptAndBodyIsNotOverwritten(t *testing.T) {
	body := "Content-Type: text/plain\r\n\r\nthe real body"
	noCIDPDF := "Content-Type: application/pdf\r\nContent-Disposition: inline; filename=\"report.pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\nJVBERg=="
	anonymousPNG := "Content-Type: image/png\r\nContent-Transfer-Encoding: base64\r\n\r\niVBORw=="
	cidPNG := "Content-Type: image/png\r\nContent-ID: <logo@x>\r\nContent-Disposition: inline; filename=\"logo.png\"\r\nContent-Transfer-Encoding: base64\r\n\r\niVBORw=="
	namedText := "Content-Type: text/plain\r\nContent-Disposition: inline; filename=\"notes.txt\"\r\n\r\nattached notes"

	for _, test := range []struct {
		name, part, filename string
		inline               bool
		contentID            string
	}{
		{"inline file without Content-ID", noCIDPDF, "report.pdf", false, ""},
		{"inline part without name or Content-ID", anonymousPNG, "attachment-1", false, ""},
		{"inline image with Content-ID", cidPNG, "logo.png", true, "logo@x"},
		{"named inline text part", namedText, "notes.txt", false, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := "From: a@example.com\r\nTo: b@example.com\r\nSubject: s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=B\r\n\r\n--B\r\n" +
				body + "\r\n--B\r\n" + test.part + "\r\n--B--\r\n"
			parsed, err := ParseMessage(context.Background(), strings.NewReader(raw), store.NewBlobStore(t.TempDir()), "account", 1)
			if err != nil || parsed == nil {
				t.Fatalf("parse: %v", err)
			}
			if parsed.TextBody != "the real body" {
				t.Fatalf("TextBody = %q", parsed.TextBody)
			}
			if len(parsed.Attachments) != 1 {
				t.Fatalf("attachments = %#v, want one", parsed.Attachments)
			}
			if a := parsed.Attachments[0]; a.Filename != test.filename || a.Inline != test.inline || a.ContentID != test.contentID {
				t.Fatalf("attachment = %#v", a)
			}
		})
	}

	t.Run("mixed text, image, text keeps both texts in order", func(t *testing.T) {
		raw := "From: a@example.com\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=B\r\n\r\n--B\r\n" +
			"Content-Type: text/plain\r\n\r\nA\r\n--B\r\n" + anonymousPNG + "\r\n--B\r\n" + "Content-Type: text/plain\r\n\r\nB\r\n--B--\r\n"
		parsed, err := ParseMessage(context.Background(), strings.NewReader(raw), store.NewBlobStore(t.TempDir()), "account", 1)
		if err != nil || parsed == nil {
			t.Fatalf("parse: %v", err)
		}
		if parsed.TextBody != "A\nB" || len(parsed.Attachments) != 1 {
			t.Fatalf("TextBody = %q, attachments = %d, want %q and 1", parsed.TextBody, len(parsed.Attachments), "A\nB")
		}
	})

	t.Run("multipart alternative sets each field once", func(t *testing.T) {
		raw := "From: a@example.com\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=B\r\n\r\n--B\r\n" +
			"Content-Type: text/plain\r\n\r\nplain\r\n--B\r\nContent-Type: text/html\r\n\r\n<p>html</p>\r\n--B--\r\n"
		parsed, err := ParseMessage(context.Background(), strings.NewReader(raw), store.NewBlobStore(t.TempDir()), "account", 1)
		if err != nil || parsed == nil {
			t.Fatalf("parse: %v", err)
		}
		if parsed.TextBody != "plain" || string(parsed.HTMLBody) != "<p>html</p>" {
			t.Fatalf("bodies = %q / %q", parsed.TextBody, parsed.HTMLBody)
		}
	})
}
