package views

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
)

// The body frame must not fade in: on macOS the web view loses the frame's
// painted content when it resizes during the fade (blank top of message).
func TestMailViewBodyFrameHasNoOpacityTransition(t *testing.T) {
	var buf bytes.Buffer
	if err := MailViewBodyByID("42").Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`<iframe[^>]*data-email-body-frame[^>]*>`).FindString(buf.String())
	if m == "" {
		t.Fatal("body frame not rendered")
	}
	if strings.Contains(m, "transition") {
		t.Fatalf("body frame has a transition class: %s", m)
	}
}
