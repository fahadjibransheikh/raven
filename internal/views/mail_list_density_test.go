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
