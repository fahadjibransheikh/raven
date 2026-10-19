package views

import (
	"strings"
	"testing"
)

func TestThreadSenderWarningBannerRendersPerMessage(t *testing.T) {
	th := threadFixture()
	th[2].SenderWarnings = []string{"This sender's domain looks like paypal.com but isn't."}
	html := renderThread(t, th, false)
	if got := strings.Count(html, "data-sender-warning-banner"); got != 1 {
		t.Fatalf("banner count = %d, want 1 (only the flagged message)", got)
	}
	if !strings.Contains(html, "looks like paypal.com") {
		t.Fatalf("banner text missing: %s", html)
	}
	if clean := renderThread(t, threadFixture(), false); strings.Contains(clean, "data-sender-warning-banner") {
		t.Fatal("banner rendered without warnings")
	}
}
