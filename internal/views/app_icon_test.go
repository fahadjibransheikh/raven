package views

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestAppIconSrcCoversEveryStyleAndAppearance(t *testing.T) {
	want := map[string][2]string{
		"raven":   {"logo-raven.svg", "logo-raven-light.svg"},
		"classic": {"logo-classic.svg", "logo-classic-light.svg"},
		"minimal": {"logo-minimal-dark.svg", "logo-minimal-light.svg"},
	}
	for style, files := range want {
		if got := appIconSrc(style, true); got != "/assets/brand/"+files[0] {
			t.Errorf("appIconSrc(%s, dark) = %s", style, got)
		}
		if got := appIconSrc(style, false); got != "/assets/brand/"+files[1] {
			t.Errorf("appIconSrc(%s, light) = %s", style, got)
		}
		for _, f := range files {
			if _, err := os.Stat(filepath.Join("..", "..", "assets", "brand", f)); err != nil {
				t.Errorf("brand file missing: %v", err)
			}
		}
	}
	if len(appIconStyles) != len(want) {
		t.Errorf("appIconStyles has %d styles, want %d", len(appIconStyles), len(want))
	}
}

func TestAppIconStyleFallsBackToRaven(t *testing.T) {
	for stored, want := range map[string]string{"raven": "raven", "classic": "classic", "minimal": "minimal", "": "raven", "bogus": "raven"} {
		if got := appIconStyle(map[string]string{"app_icon": stored}); got != want {
			t.Errorf("appIconStyle(%q) = %q, want %q", stored, got, want)
		}
	}
	if appIconStyle(nil) != "raven" {
		t.Error("nil settings are not raven")
	}
	if !IsAppIconStyle("minimal") || IsAppIconStyle("") || IsAppIconStyle("Raven") {
		t.Error("IsAppIconStyle accepts the wrong values")
	}
}

func TestPageIconFollowsStyleAndRenderedTheme(t *testing.T) {
	for _, tc := range []struct {
		settings map[string]string
		want     string
	}{
		{nil, "/assets/brand/logo-raven.svg"},
		{map[string]string{"app_icon": "classic"}, "/assets/brand/logo-classic.svg"},
		{map[string]string{"app_icon": "classic", "theme": "light"}, "/assets/brand/logo-classic-light.svg"},
		{map[string]string{"app_icon": "minimal", "theme": "dark"}, "/assets/brand/logo-minimal-dark.svg"},
		{map[string]string{"app_icon": "minimal", "theme": "light"}, "/assets/brand/logo-minimal-light.svg"},
	} {
		var out bytes.Buffer
		if err := AppIconFor(tc.settings).Render(context.Background(), &out); err != nil {
			t.Fatal(err)
		}
		html := out.String()
		if !strings.Contains(html, `rel="icon"`) || !strings.Contains(html, `href="`+tc.want+`"`) {
			t.Errorf("AppIconFor(%v) = %s, want icon %s", tc.settings, html, tc.want)
		}
		// The home-screen icons are deliberately not themable.
		for _, keep := range []string{`href="/assets/manifest.webmanifest"`, `rel="apple-touch-icon" href="/assets/apple-touch-icon.png"`} {
			if !strings.Contains(html, keep) {
				t.Errorf("AppIconFor(%v) lost %s", tc.settings, keep)
			}
		}
	}
}

func TestSettingsAppearanceOffersThreeAppIcons(t *testing.T) {
	var out bytes.Buffer
	if err := SettingsAppearanceTab(map[string]string{"app_icon": "classic"}).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, style := range []string{"raven", "classic", "minimal"} {
		if !strings.Contains(html, `name="app_icon"`) || !regexp.MustCompile(`name="app_icon"[^>]*value="`+style+`"|value="`+style+`"[^>]*name="app_icon"`).MatchString(html) {
			t.Errorf("no app_icon radio for %s", style)
		}
		for _, dark := range []bool{true, false} {
			if !strings.Contains(html, `src="`+appIconSrc(style, dark)+`"`) {
				t.Errorf("no tile image %s", appIconSrc(style, dark))
			}
		}
	}
	checked := regexp.MustCompile(`<input[^>]*name="app_icon"[^>]*>`).FindAllString(html, -1)
	if len(checked) != 3 {
		t.Fatalf("found %d app_icon radios, want 3", len(checked))
	}
	checkedAttr := regexp.MustCompile(`\schecked[\s>=]`)
	for _, input := range checked {
		isClassic := strings.Contains(input, `value="classic"`)
		if hasChecked := checkedAttr.MatchString(input); hasChecked != isClassic {
			t.Errorf("checked state wrong for %s", input)
		}
	}
	if !strings.Contains(html, "Dock icon change") || !strings.Contains(html, "Finder and the phone home-screen icon keep the default") {
		t.Error("missing the Dock / home-screen help line")
	}
}
