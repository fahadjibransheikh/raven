package message

import (
	"context"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/store"
)

func TestParseListUnsubscribe(t *testing.T) {
	const oneClick = "List-Unsubscribe=One-Click"
	cases := []struct {
		name, header, post string
		want               Unsubscribe
		method             string
	}{
		{"both", "<mailto:unsub@x.com?subject=unsubscribe>, <https://x.com/u?id=1>", oneClick,
			Unsubscribe{HTTPS: "https://x.com/u?id=1", Mailto: "mailto:unsub@x.com?subject=unsubscribe", OneClick: true}, "one-click"},
		{"https without post header is browser-only", "<https://x.com/u>", "",
			Unsubscribe{HTTPS: "https://x.com/u"}, "browser"},
		{"mailto beats browser-only https", "<https://x.com/u>, <mailto:u@x.com>", "",
			Unsubscribe{HTTPS: "https://x.com/u", Mailto: "mailto:u@x.com"}, "mailto"},
		{"post header without https is not one-click", "<mailto:u@x.com>", oneClick,
			Unsubscribe{Mailto: "mailto:u@x.com"}, "mailto"},
		{"http ignored", "<http://x.com/u>", oneClick, Unsubscribe{}, ""},
		{"folded whitespace", "<https://x.com/\r\n u?id=1>,\r\n <mailto:u@x.com>", "  " + oneClick + " ",
			Unsubscribe{HTTPS: "https://x.com/u?id=1", Mailto: "mailto:u@x.com", OneClick: true}, "one-click"},
		{"no angle brackets ignored", "https://x.com/u, mailto:u@x.com", oneClick, Unsubscribe{}, ""},
		{"comment and first of each kind wins", "(Unsub) <https://a.com/1>, <https://b.com/2>, <mailto:a@a.com>, <mailto:b@b.com>", "",
			Unsubscribe{HTTPS: "https://a.com/1", Mailto: "mailto:a@a.com"}, "mailto"},
		{"empty", "", "", Unsubscribe{}, ""},
		{"https without host", "<https:///x>", oneClick, Unsubscribe{}, ""},
	}
	for _, tc := range cases {
		got := ParseListUnsubscribe(tc.header, tc.post)
		if got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
		if got.Method() != tc.method {
			t.Errorf("%s: method %q, want %q", tc.name, got.Method(), tc.method)
		}
	}
}

func TestMailtoRequest(t *testing.T) {
	to, subj, body, ok := MailtoRequest("mailto:unsub@x.com?subject=Remove%20me&body=please")
	if !ok || to.Address != "unsub@x.com" || subj != "Remove me" || body != "please" {
		t.Errorf("got %v %q %q %v", to, subj, body, ok)
	}
	if _, subj, _, ok := MailtoRequest("mailto:u@x.com"); !ok || subj != "unsubscribe" {
		t.Errorf("default subject = %q ok=%v", subj, ok)
	}
	if _, subj, _, _ := MailtoRequest("mailto:u@x.com?subject=a%0D%0ABcc:%20evil@x.com"); subj != "a Bcc: evil@x.com" {
		t.Errorf("newlines not collapsed: %q", subj)
	}
	if _, _, _, ok := MailtoRequest("mailto:?subject=x"); ok {
		t.Error("empty address accepted")
	}
	if _, _, _, ok := MailtoRequest("https://x.com"); ok {
		t.Error("non-mailto accepted")
	}
}

func TestParseMessageCapturesListUnsubscribeHeaders(t *testing.T) {
	raw := "From: n@example.invalid\r\nSubject: hi\r\nDelivered-To: me@example.invalid\r\nX-Original-To: alias@example.invalid\r\nList-Unsubscribe: <mailto:u@example.invalid>,\r\n <https://example.invalid/u>\r\nList-Unsubscribe-Post: List-Unsubscribe=One-Click\r\nContent-Type: text/plain\r\n\r\nbody\r\n"
	parsed, err := ParseMessage(context.Background(), strings.NewReader(raw), store.NewBlobStore(t.TempDir()), "acc", 1)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.DeliveredTo != "alias@example.invalid,me@example.invalid" {
		t.Errorf("DeliveredTo = %q", parsed.DeliveredTo)
	}
	u := ParseListUnsubscribe(parsed.ListUnsubscribe, parsed.ListUnsubscribePost)
	if !u.OneClick || u.Mailto != "mailto:u@example.invalid" || u.HTTPS != "https://example.invalid/u" {
		t.Errorf("got %+v from %q / %q", u, parsed.ListUnsubscribe, parsed.ListUnsubscribePost)
	}
}

func TestCaptureDeliveredTo(t *testing.T) {
	if got := CaptureDeliveredTo(" a@x.com ", "Alias <B@x.com>", "A@X.com", "", "bad addr"); got != "a@x.com,B@x.com" {
		t.Errorf("got %q", got)
	}
	if got := CaptureDeliveredTo(); got != "" {
		t.Errorf("empty = %q", got)
	}
}
