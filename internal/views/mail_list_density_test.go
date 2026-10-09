package views

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

func TestMailListDensity(t *testing.T) {
	for _, tc := range []struct {
		settings map[string]string
		want     string
	}{
		{nil, "calm"},
		{map[string]string{"mail_list_density": ""}, "calm"},
		{map[string]string{"mail_list_density": "calm"}, "calm"},
		{map[string]string{"mail_list_density": "airy"}, "airy"},
		{map[string]string{"mail_list_density": "huge"}, "calm"},
	} {
		if got := mailListDensity(tc.settings); got != tc.want {
			t.Errorf("mailListDensity(%v) = %q, want %q", tc.settings, got, tc.want)
		}
	}
}

// The default layout puts subject and preview in the same body zone, which the
// calm density renders as one truncating line.
func TestMailListCardRendersSubjectAndPreviewInOneBodyZone(t *testing.T) {
	var out bytes.Buffer
	email := models.Email{ID: "m1", Subject: "Quarterly review", Preview: "Thanks for the draft", IsRead: true, FolderRole: "inbox"}
	if err := MailListCardItem(nil, email, 0, nil, "name").Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	zone := func(name string) string {
		m := regexp.MustCompile(`(?s)data-mail-card-zone="` + name + `">(.*?)</div>\s*<div class="mail-list-card-zone`).FindStringSubmatch(html)
		if m == nil {
			t.Fatalf("zone %s not found in %s", name, html)
		}
		return m[1]
	}
	body, footer := zone("body"), zone("footer")
	for _, field := range []string{`data-mail-card-field="subject"`, `data-mail-card-field="preview"`} {
		if !strings.Contains(body, field) {
			t.Errorf("body zone is missing %s: %s", field, body)
		}
	}
	if strings.Contains(footer, `data-mail-card-field="preview"`) {
		t.Errorf("preview is still in the footer zone: %s", footer)
	}
}

// Default layout: text-only rows. The avatar sits in the hidden zone, and the
// account marker is a colour bar that only renders with more than one account.
func TestMailListCardDefaultsToTextOnlyWithAccountBar(t *testing.T) {
	render := func(accounts []models.Account) string {
		var out bytes.Buffer
		email := models.Email{ID: "m1", AccountID: "a1", AccountColor: "#112233", Subject: "Hi", From: models.Contact{Name: "Ann", Email: "ann@example.com", Initials: "A"}, IsRead: true, Date: "21:01"}
		if err := MailListCardItem(accounts, email, 0, nil, "name").Render(context.Background(), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	// zone is the markup from a zone's opening attribute up to the next zone's.
	zone := func(html, name string) string {
		_, rest, ok := strings.Cut(html, `data-mail-card-zone="`+name+`"`)
		if !ok {
			t.Fatalf("zone %s not found", name)
		}
		head, _, _ := strings.Cut(rest, `data-mail-card-zone=`)
		return head
	}
	html := render([]models.Account{{ID: "a1"}, {ID: "a2"}})
	if got := zone(html, "railTop"); strings.Contains(got, "avatar") {
		t.Errorf("railTop holds an avatar by default: %s", got)
	}
	if got := zone(html, "hidden"); !strings.Contains(got, `data-mail-card-field="avatar"`) {
		t.Errorf("avatar is not in the hidden zone: %s", got)
	}
	if got := zone(html, "railMiddle"); !strings.Contains(got, "mail-list-account-bar") || !strings.Contains(got, "background-color: #112233") {
		t.Errorf("account bar missing or wrong colour: %s", got)
	}
	if got := zone(render([]models.Account{{ID: "a1"}}), "railMiddle"); strings.Contains(got, "mail-list-account-bar") {
		t.Errorf("single-account rows render the bar: %s", got)
	}
	// Calm weights and monospace time.
	for _, bad := range []string{"font-semibold", "font-bold", "font-medium"} {
		for _, field := range []string{`data-mail-card-field="from"`, `data-mail-card-field="subject"`} {
			i := strings.Index(html, field)
			j := strings.LastIndex(html[:i], "<")
			if strings.Contains(html[j:i], bad) {
				t.Errorf("%s has %s: %s", field, bad, html[j:i])
			}
		}
	}
	if !regexp.MustCompile(`font-mono[^>]*data-mail-card-field="date"`).MatchString(html) {
		t.Errorf("date is not monospace")
	}
}

func TestSettingsAppearanceOffersBothDensities(t *testing.T) {
	var out bytes.Buffer
	if err := SettingsAppearanceTab(map[string]string{"mail_list_density": "airy"}).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{`name="mail_list_density"`, `value="calm"`, `value="airy"`, "Two lines, more on screen", "Roomier rows"} {
		if !strings.Contains(html, want) {
			t.Errorf("appearance tab is missing %q", want)
		}
	}
	if !regexp.MustCompile(`value="airy"[^>]*checked|checked[^>]*value="airy"`).MatchString(html) {
		t.Error("airy is not the checked option when the setting is airy")
	}
}
