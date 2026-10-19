package views

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/models"
)

func TestComposeToolbarButtonsAllHaveNames(t *testing.T) {
	var buf bytes.Buffer
	if err := ComposeToolbar().Render(context.Background(), &buf); err != nil {
		t.Fatalf("ComposeToolbar.Render() error = %v", err)
	}
	// The icon buttons share the size-7 class; dropdown items are buttons with visible text.
	buttons := regexp.MustCompile(`<button[^>]*size-7[^>]*>`).FindAllString(buf.String(), -1)
	if len(buttons) != 11 {
		t.Fatalf("want 11 toolbar icon buttons, got %d", len(buttons))
	}
	for _, b := range buttons {
		if !strings.Contains(b, `aria-label="`) {
			t.Fatalf("toolbar button has no accessible name: %s", b)
		}
	}
}

func TestComposeRecipientFieldIsNamed(t *testing.T) {
	var buf bytes.Buffer
	if err := ComposeField("Cc", "cc", "").Render(context.Background(), &buf); err != nil {
		t.Fatalf("ComposeField.Render() error = %v", err)
	}
	if html := buf.String(); !strings.Contains(html, `role="textbox" aria-label="Cc"`) {
		t.Fatalf("recipient textbox should be named after its field: %s", html)
	}
}

func TestSkipToMainTargetsTheMainRegion(t *testing.T) {
	var buf bytes.Buffer
	if err := SkipToMain().Render(context.Background(), &buf); err != nil {
		t.Fatalf("SkipToMain.Render() error = %v", err)
	}
	if html := buf.String(); !strings.Contains(html, `href="#main-content"`) {
		t.Fatalf("skip link should jump to #main-content: %s", html)
	}
	buf.Reset()
	if err := MailShell(nil, "inbox", nil, nil, 0, 0, map[string]string{}, nil, "", 0, models.EmailFilters{}).Render(context.Background(), &buf); err != nil {
		t.Fatalf("MailShell.Render() error = %v", err)
	}
	if html := buf.String(); !strings.Contains(html, `id="main-content" role="main" tabindex="-1"`) {
		t.Fatalf("mail shell main region should be a focusable main landmark")
	}
}
