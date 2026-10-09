package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

func renderReplyBar(t *testing.T, email *models.Email) string {
	t.Helper()
	var b bytes.Buffer
	if err := MailViewReplyBar(email).Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestReplyBarShowsUnsubscribeOnlyWhenTheMessageHasAMethod(t *testing.T) {
	from := models.Contact{Name: "Acme News", Email: "news@example.invalid"}
	// No stored headers (older message): rendered hidden for app.js to reveal once the body is fetched.
	if out := renderReplyBar(t, &models.Email{ID: "1", From: from}); !strings.Contains(out, `data-unsubscribe-method=""`) || !strings.Contains(out, "hidden") {
		t.Error("expected a hidden placeholder button when headers are not stored yet")
	}
	// http: only and a bare (unbracketed) URI are not usable.
	if out := renderReplyBar(t, &models.Email{ID: "1", From: from, ListUnsubscribe: "<http://example.invalid/u>, https://example.invalid/v"}); strings.Contains(out, "data-unsubscribe-id") {
		t.Error("button rendered for unusable List-Unsubscribe")
	}

	cases := []struct {
		name, header, post, method, target string
	}{
		{"one-click", "<https://example.invalid/u>", "List-Unsubscribe=One-Click", "one-click", ""},
		{"mailto", "<mailto:unsub@example.invalid?subject=x>", "", "mailto", "unsub@example.invalid"},
		{"browser", "<https://example.invalid/u>", "", "browser", ""},
	}
	for _, tc := range cases {
		out := renderReplyBar(t, &models.Email{ID: "42", From: from, ListUnsubscribe: tc.header, ListUnsubscribePost: tc.post})
		for _, want := range []string{
			`data-unsubscribe-id="42"`, `data-unsubscribe-method="` + tc.method + `"`,
			`data-unsubscribe-target="` + tc.target + `"`, `data-unsubscribe-sender="Acme News"`, ">Unsubscribe</span>",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: missing %q", tc.name, want)
			}
		}
		if strings.Index(out, "Unsubscribe</span>") > strings.Index(out, ">Forward</span>") {
			t.Errorf("%s: Unsubscribe should sit before Forward", tc.name)
		}
		if strings.Contains(out[strings.Index(out, "data-unsubscribe-id")-300:strings.Index(out, "data-unsubscribe-id")], " hidden") {
			t.Errorf("%s: usable button must not be hidden", tc.name)
		}
	}
}
