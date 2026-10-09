package views

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

func TestThreadUnsubscribeTargetsNewestMessageWithUsableData(t *testing.T) {
	const ok = "<https://example.invalid/u>"
	th := threadFixture() // m1..m4, oldest first
	th[1].ListUnsubscribe = ok
	th[2].ListUnsubscribe = "<http://example.invalid/insecure>" // present but unusable
	th[3].ListUnsubscribe = "<http://example.invalid/insecure>"
	slices.Reverse(th) // newest first
	out := renderThread(t, th, true)
	if n := strings.Count(out, "data-unsubscribe-id"); n != 1 || !strings.Contains(out, `data-unsubscribe-id="m2"`) || !strings.Contains(out, `data-unsubscribe-method="browser"`) {
		t.Errorf("want one Unsubscribe button targeting m2, got %d:\n%s", n, out)
	}

	// Newest usable wins over an older usable one, in either display order.
	th[0].ListUnsubscribe = ok
	for _, newestFirst := range []bool{true, false} {
		d := slices.Clone(th)
		if !newestFirst {
			slices.Reverse(d)
		}
		if got := ThreadUnsubscribeID(d, newestFirst); got != "m4" {
			t.Errorf("newestFirst=%v: got %q want m4", newestFirst, got)
		}
	}

	// Nothing stored anywhere: hidden placeholder on the newest for app.js to reveal.
	bare := threadFixture()
	slices.Reverse(bare)
	if got := ThreadUnsubscribeID(bare, true); got != "m4" {
		t.Errorf("placeholder: got %q want m4", got)
	}
	var b bytes.Buffer
	_ = MailViewContent(&models.Email{ID: "m4"}, bare, true).Render(context.Background(), &b)
	if !strings.Contains(b.String(), `data-unsubscribe-method=""`) {
		t.Error("expected hidden placeholder button")
	}

	// Newest has headers but none usable, and no older one is: no button.
	none := threadFixture()
	none[3].ListUnsubscribe = "<http://example.invalid/insecure>"
	slices.Reverse(none)
	if got := ThreadUnsubscribeID(none, true); got != "" {
		t.Errorf("unusable: got %q want none", got)
	}
}

func TestHeaderMoreMenuItemsAreWired(t *testing.T) {
	var b bytes.Buffer
	if err := MailViewHeader(&models.Email{ID: "9"}).Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{`handleReply(null, &#39;reply&#39;)`, `handleReply(null, &#39;forward&#39;)`, `data-refetch-email="9"`, `downloadMessageRaw(&#39;9&#39;)`, `printMessage(&#39;9&#39;)`} {
		if !strings.Contains(out, want) {
			t.Errorf("More menu missing %s", want)
		}
	}
}
