package legacyenv

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

func TestLookupPrefersCurrentName(t *testing.T) {
	t.Setenv("RAVEN_LEGACYENV_X", "new")
	t.Setenv("GOFER_LEGACYENV_X", "old")
	if got, ok := Lookup("RAVEN_LEGACYENV_X"); !ok || got != "new" {
		t.Fatalf("Lookup = %q, %v; want new, true", got, ok)
	}
}

func TestLookupFallsBackToLegacyName(t *testing.T) {
	t.Setenv("GOFER_LEGACYENV_Y", "old")
	if got := Get("RAVEN_LEGACYENV_Y"); got != "old" {
		t.Fatalf("Get = %q; want old", got)
	}
}

func TestLookupEmptyCurrentNameWinsOverLegacy(t *testing.T) {
	t.Setenv("RAVEN_LEGACYENV_Z", "")
	t.Setenv("GOFER_LEGACYENV_Z", "old")
	if got, ok := Lookup("RAVEN_LEGACYENV_Z"); !ok || got != "" {
		t.Fatalf("Lookup = %q, %v; want empty, true", got, ok)
	}
}

func TestLookupUnsetAndForeignNames(t *testing.T) {
	if _, ok := Lookup("RAVEN_LEGACYENV_UNSET"); ok {
		t.Fatal("unset variable reported as set")
	}
	t.Setenv("GOFER_LEGACYENV_FOREIGN", "x")
	if _, ok := Lookup("OTHER_LEGACYENV_FOREIGN"); ok {
		t.Fatal("non-RAVEN names must not fall back")
	}
}

func TestLogLegacyNamesUsedAndShadowedWithoutValues(t *testing.T) {
	for _, entry := range os.Environ() {
		if key, _, _ := strings.Cut(entry, "="); strings.HasPrefix(key, "GOFER_") {
			t.Setenv(key, "")
			os.Unsetenv(key)
		}
	}
	t.Setenv("GOFER_LEGACYENV_USED", "secret-value")
	t.Setenv("GOFER_LEGACYENV_SHADOWED", "secret-value")
	t.Setenv("RAVEN_LEGACYENV_SHADOWED", "new")
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	LogLegacy()
	out := buf.String()
	if !strings.Contains(out, "using legacy environment names GOFER_LEGACYENV_USED") {
		t.Fatalf("missing used-name line: %q", out)
	}
	if !strings.Contains(out, "ignoring legacy environment names GOFER_LEGACYENV_SHADOWED") {
		t.Fatalf("missing shadowed-name line: %q", out)
	}
	if strings.Contains(out, "secret-value") {
		t.Fatalf("log leaked a value: %q", out)
	}
}
