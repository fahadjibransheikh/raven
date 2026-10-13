package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

func TestThreadDraftsOfferToContinueEditing(t *testing.T) {
	render := func(item models.ThreadItem) string {
		var out bytes.Buffer
		if err := MailViewThreadItemActions(&item, false, false).Render(context.Background(), &out); err != nil {
			t.Fatalf("Render() error = %v", err)
		}
		return out.String()
	}
	draft := render(models.ThreadItem{ID: "602", IsDraft: true})
	for _, want := range []string{"Continue editing", "Discard draft", "continueEditingDraft", "discardReadPaneDraft", "602"} {
		if !strings.Contains(draft, want) {
			t.Fatalf("draft actions missing %q: %s", want, draft)
		}
	}
	if strings.Contains(draft, "handleReply") {
		t.Fatalf("a draft offers to reply to itself: %s", draft)
	}
	sent := render(models.ThreadItem{ID: "601"})
	if strings.Contains(sent, "continueEditingDraft") {
		t.Fatalf("a sent message offers to continue editing: %s", sent)
	}
}

// Reply, reply all and forward sit in each message's header row, so they work while
// the message is collapsed; a draft in the thread gets none of them.
func TestThreadMessagesHaveQuickReplyButtons(t *testing.T) {
	render := func(item models.ThreadItem, newest bool) string {
		var out bytes.Buffer
		if err := MailViewThreadItemMiniHeader(&item, newest).Render(context.Background(), &out); err != nil {
			t.Fatalf("Render() error = %v", err)
		}
		return out.String()
	}
	sent := render(models.ThreadItem{ID: "601", InternetMessageID: "<a@x>", AccountID: "acc"}, true)
	for _, want := range []string{`[&#34;handleReply&#34;,&#34;$this&#34;,&#34;reply&#34;]`, `[&#34;handleReply&#34;,&#34;$this&#34;,&#34;reply-all&#34;]`, `[&#34;handleReply&#34;,&#34;$this&#34;,&#34;forward&#34;]`, "data-thread-reply-data", "data-thread-newest", `data-message-id="&lt;a@x&gt;"`, `data-account-id="acc"`} {
		if !strings.Contains(sent, want) {
			t.Errorf("quick reply missing %q: %s", want, sent)
		}
	}
	if older := render(models.ThreadItem{ID: "600"}, false); strings.Contains(older, "data-thread-newest") {
		t.Errorf("an older message must not be the reply target: %s", older)
	}
	if draft := render(models.ThreadItem{ID: "602", IsDraft: true}, true); strings.Contains(draft, "handleReply") {
		t.Errorf("a draft offers to reply to itself: %s", draft)
	}
	// The full-width buttons at the end of an opened message are gone.
	var out bytes.Buffer
	if err := MailViewThreadItemActions(&models.ThreadItem{ID: "601"}, true, false).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "handleReply") {
		t.Errorf("reply buttons still repeat under the body: %s", out.String())
	}
}
