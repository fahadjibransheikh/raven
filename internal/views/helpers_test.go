package views

import "testing"

func TestThemeStyleMapsLegacyAndUnknownValues(t *testing.T) {
	for stored, want := range map[string]string{"raven": "raven", "classic": "classic", "minimal": "minimal", "": "raven", "skeuo": "raven"} {
		if got := themeStyle(map[string]string{"theme_style": stored}); got != want {
			t.Errorf("themeStyle(%q) = %q, want %q", stored, got, want)
		}
	}
}
