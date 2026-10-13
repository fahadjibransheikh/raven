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
	if !strings.Contains(sent, "handleReply") || strings.Contains(sent, "continueEditingDraft") {
		t.Fatalf("a sent message got the wrong actions: %s", sent)
	}
}
