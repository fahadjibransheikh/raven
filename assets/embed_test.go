//go:build embedded_assets

package assets

import "testing"

// The app icon picker serves these from /assets/brand/ in release builds.
func TestBrandIconsAreEmbedded(t *testing.T) {
	for _, name := range []string{"logo-raven", "logo-raven-light", "logo-classic", "logo-classic-light", "logo-minimal-dark", "logo-minimal-light"} {
		if _, err := FS.ReadFile("brand/" + name + ".svg"); err != nil {
			t.Errorf("brand/%s.svg is not embedded: %v", name, err)
		}
		if _, err := FS.ReadFile("brand/touch-" + name + ".png"); err != nil {
			t.Errorf("brand/touch-%s.png is not embedded: %v", name, err)
		}
	}
}
