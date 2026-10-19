package mail

import (
	"context"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/storage"
)

func TestPublishNewMailCarriesSignInCodeFromSubject(t *testing.T) {
	db := newLabelSyncTestDB(t)
	seedLabelSyncMessage(t, db, "imap", "<m1@example.com>", "")
	orchestrator := NewSyncOrchestrator(db, nil, nil, nil)
	ch := orchestrator.events.Subscribe()

	publish := func(subject string) map[string]any {
		var summary newMailSummary
		// IMAP sync sets Snippet to the subject.
		summary.Add([]storage.SyncMessage{{Subject: subject, Snippet: subject, FromEmail: "sender@example.com", DateSent: time.Now()}})
		orchestrator.publishNewMail(context.Background(), "acc", "acc_inbox", "INBOX", summary)
		select {
		case event := <-ch:
			return event.Payload
		case <-time.After(time.Second):
			t.Fatal("no new-mail event published")
			return nil
		}
	}

	if got := publish("Your verification code is 482913")["code"]; got != "482913" {
		t.Fatalf(`payload["code"] = %v, want 482913`, got)
	}
	if _, ok := publish("Your order 482913 has shipped")["code"]; ok {
		t.Fatal(`payload["code"] set for a mail without a sign-in code`)
	}
}
