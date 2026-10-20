package views

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/models"
)

func TestUnreadFilterHiddenInSentAndDrafts(t *testing.T) {
	for folder, want := range map[string]bool{"inbox": true, "sent": false, "drafts": false} {
		var out bytes.Buffer
		if err := MailListToolbar(nil, folder, "cards", models.EmailFilters{}).Render(t.Context(), &out); err != nil {
			t.Fatal(err)
		}
		html := out.String()
		tag := html[strings.LastIndex(html[:strings.Index(html, "data-mail-unread-toggle")], "<button"):strings.Index(html, "data-mail-unread-toggle")]
		if got := !strings.Contains(tag, "hidden"); got != want {
			t.Errorf("%s: unread toggle shown = %v, want %v", folder, got, want)
		}
	}
	// A filter left on in Sent keeps its toggle so it can be turned off.
	var out bytes.Buffer
	if err := MailListToolbar(nil, "sent", "cards", models.EmailFilters{Unread: true}).Render(t.Context(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if tag := html[strings.LastIndex(html[:strings.Index(html, "data-mail-unread-toggle")], "<button"):strings.Index(html, "data-mail-unread-toggle")]; strings.Contains(tag, "hidden") {
		t.Error("an active Unread filter must stay visible to turn it off")
	}
}

func TestThemeModeSystem(t *testing.T) {
	for stored, want := range map[string]string{"": "dark", "dark": "dark", "light": "light", "system": "system", "bogus": "dark"} {
		settings := map[string]string{}
		if stored != "" {
			settings["theme"] = stored
		}
		if got := themeMode(settings); got != want {
			t.Errorf("themeMode(%q) = %q, want %q", stored, got, want)
		}
	}
	// The server can't see the OS preference, so System renders without the dark class.
	if got := themeClass(map[string]string{"theme": "system"}); got != "" {
		t.Errorf("themeClass(system) = %q, want none", got)
	}
}

func TestAddAccountContactsSyncOffByDefault(t *testing.T) {
	var out bytes.Buffer
	if err := AccountWizardAddSidebar().Render(t.Context(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	start := strings.Index(html, "data-add-contact-sync-switch")
	if start < 0 {
		t.Fatal("contacts switch missing")
	}
	tagStart := strings.LastIndex(html[:start], "<input")
	tagEnd := strings.Index(html[start:], ">") + start
	if tagStart >= 0 && strings.Contains(html[tagStart:tagEnd], "checked") {
		t.Error("contacts sync should be off by default for a new account")
	}
}
