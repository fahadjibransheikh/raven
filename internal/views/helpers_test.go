package views

import (
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

func TestThemeStyleMapsLegacyAndUnknownValues(t *testing.T) {
	for stored, want := range map[string]string{"raven": "raven", "classic": "classic", "minimal": "minimal", "": "raven", "skeuo": "raven"} {
		if got := themeStyle(map[string]string{"theme_style": stored}); got != want {
			t.Errorf("themeStyle(%q) = %q, want %q", stored, got, want)
		}
	}
}

func TestContactAvatarToneIsStablePerSender(t *testing.T) {
	a := contactAvatarTone(models.Contact{Email: "Priya@Northwind.io"})
	if b := contactAvatarTone(models.Contact{Email: " priya@northwind.io "}); a != b {
		t.Fatalf("tone differs by case/space: %s vs %s", a, b)
	}
	seen := map[string]bool{}
	for _, e := range []string{"a@x.com", "b@x.com", "c@x.com", "d@x.com", "e@x.com", "f@x.com", "g@x.com", "h@x.com", "i@x.com", "j@x.com", "k@x.com", "l@x.com"} {
		seen[contactAvatarTone(models.Contact{Email: e})] = true
	}
	if len(seen) < 3 {
		t.Fatalf("tones barely vary: %v", seen)
	}
}
