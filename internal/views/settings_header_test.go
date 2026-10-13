package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/models"
)

func TestSettingsSectionHeadersNameTheirPage(t *testing.T) {
	cases := map[string]string{
		"accounts":        "Connect and manage your email and calendar accounts.",
		"sync":            "Email sync and folders",
		"operations":      "Mail operations",
		"contacts":        "Manage local contacts and sync contacts from connected accounts.",
		"appearance":      "Choose the visual style and color mode for Raven.",
		"regional":        "Choose the time zone used for message dates and scheduled sends.",
		"compose-display": "Compose and display",
		"security":        "Manage sign-in methods, active sessions, and security activity.",
		"security-open":   "Choose which mail servers Raven may connect to.",
		"advanced":        "Configure prefetching, notifications, and email link handling.",
	}
	for section, want := range cases {
		var out bytes.Buffer
		if err := SettingsSectionHeader(section).Render(context.Background(), &out); err != nil {
			t.Fatalf("%s: %v", section, err)
		}
		html := out.String()
		if !strings.Contains(html, want) {
			t.Errorf("%s header missing %q: %s", section, want, html)
		}
		if strings.Contains(html, "Manage your accounts and preferences") {
			t.Errorf("%s header should be section specific: %s", section, html)
		}
		if !strings.Contains(html, "data-phone-nav-toggle") {
			t.Errorf("%s header lost the phone drawer button", section)
		}
	}
}

func TestSettingsPageUsesItsSectionHeader(t *testing.T) {
	var out bytes.Buffer
	if err := SettingsPage(nil, models.SyncSettings{}, "regional", map[string]string{}, nil).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if !strings.Contains(html, "<h2") || !strings.Contains(html, ">Regional</h2>") {
		t.Errorf("regional page should be titled Regional: %s", html[:min(len(html), 400)])
	}
}

func TestSettingsCardsSplitRowsWithThinLines(t *testing.T) {
	var out bytes.Buffer
	if err := SettingsComposeDisplayTab(map[string]string{}, nil).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if n := strings.Count(html, "settings-card"); n < 3 {
		t.Errorf("want settings-card sections, got %d", n)
	}
	if n := strings.Count(html, "settings-row border-t border-border/60"); n < 6 {
		t.Errorf("rows inside cards should be split by thin lines, got %d", n)
	}
}
