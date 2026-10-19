package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/SherClockHolmes/webpush-go"
)

func TestNewMailNotificationTextPutsSignInCodeFirst(t *testing.T) {
	payload := map[string]any{"from_name": "Acme", "subject": "Your sign-in code", "code": "123456", "folder_name": "Inbox"}
	title, body := newMailNotificationText(payload, 1)
	if title != "Acme" || body != "Code 123456 · Your sign-in code" {
		t.Fatalf("single = %q / %q", title, body)
	}
	title, body = newMailNotificationText(payload, 3)
	if title != "3 new messages" || body != "Code 123456 · Acme: Your sign-in code - Inbox" {
		t.Fatalf("grouped = %q / %q", title, body)
	}
	delete(payload, "code")
	if _, body = newMailNotificationText(payload, 1); body != "Your sign-in code" {
		t.Fatalf("without a code the body changed: %q", body)
	}
}

func TestWebPushBodyLeadsWithSignInCode(t *testing.T) {
	f := newUserStorageFixture(t)
	f.subscribe(t, "alice", "https://push.example/alice")
	var pushed map[string]any
	f.service.sendNotification = func(_ context.Context, body []byte, _ *webpush.Subscription, _ *webpush.Options) (*http.Response, error) {
		_ = json.Unmarshal(body, &pushed)
		return pushResponse(http.StatusCreated), nil
	}
	event := f.event("alice")
	event.Payload = map[string]any{"unread_count": 1, "from_name": "Acme", "subject": "Your sign-in code", "code": "123456"}
	f.service.handleNewMail(t.Context(), event)
	if pushed["body"] != "Code 123456 · Your sign-in code" {
		t.Fatalf("pushed body = %v", pushed["body"])
	}
}
