package views

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The message iframe must run only our own (CSP-nonced) scripts and must never
// be allowed to open unsandboxed popups on the app origin.
func TestEmailBodyFrameSandboxIsScriptsOnly(t *testing.T) {
	var out bytes.Buffer
	if err := MailViewBodyByID("42").Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `sandbox="allow-scripts"`) {
		t.Fatalf("iframe sandbox must be exactly allow-scripts: %s", out.String())
	}
	if strings.Contains(out.String(), "popups") || strings.Contains(out.String(), "same-origin") {
		t.Fatalf("iframe sandbox grants popups/same-origin: %s", out.String())
	}
}
