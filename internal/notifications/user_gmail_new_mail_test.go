package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/mail"
	"github.com/fahadjibransheikh/raven/internal/mailauth"
	"github.com/fahadjibransheikh/raven/internal/providers"
)

// In managed mode a Gmail history pass announces mail it stored for the first
// time, to its owner only; the first sync's import stays quiet.
func TestUserGmailHistoryAnnouncesNewInboxMail(t *testing.T) {
	f := newUserStorageFixtureMode(t, true)
	var phase atomic.Int32 // 0: first sync; 1: history brings m2
	message := func(w http.ResponseWriter, id, subject string, labels []string) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "threadId": id, "historyId": "200", "labelIds": labels, "internalDate": "1791288000000",
			"payload": map[string]any{"headers": []map[string]string{{"name": "Message-ID", "value": "<" + id + "@gmail.test>"}, {"name": "Subject", "value": subject}, {"name": "From", "value": "sender@gmail.test"}}}})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/users/me/labels":
			_ = json.NewEncoder(w).Encode(map[string]any{"labels": []map[string]string{{"id": "INBOX", "name": "INBOX", "type": "system"}, {"id": "UNREAD", "name": "UNREAD", "type": "system"}}})
		case "/users/me/profile":
			_ = json.NewEncoder(w).Encode(map[string]string{"historyId": "100"})
		case "/users/me/history":
			if phase.Load() == 0 {
				_ = json.NewEncoder(w).Encode(map[string]string{"historyId": "100"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"historyId": "200", "history": []map[string]any{{
				"messagesAdded": []map[string]any{{"message": map[string]any{"id": "m2", "labelIds": []string{"INBOX", "UNREAD"}}}},
			}}})
		case "/users/me/messages":
			messages := []map[string]string{}
			if r.URL.Query().Get("labelIds") == "INBOX" {
				messages = append(messages, map[string]string{"id": "m1"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"messages": messages})
		case "/users/me/messages/m1":
			message(w, "m1", "Old mail", []string{"INBOX"})
		case "/users/me/messages/m2":
			message(w, "m2", "Fresh mail", []string{"INBOX", "UNREAD"})
		default:
			http.Error(w, "unexpected endpoint", 404)
		}
	}))
	t.Cleanup(server.Close)
	address, _ := url.Parse(server.URL)
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: userGmailRouteTransport{target: address, base: http.DefaultTransport}}
	t.Cleanup(func() { http.DefaultClient = previous })
	ctx, cancel := context.WithCancel(t.Context())
	credentials, err := mailauth.NewUserCredentials(ctx, nil, f.routing, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { f.stopIMAP(); f.imap.Wait(); cancel(); credentials.Wait() })
	if err := f.imap.SetCredentials(credentials); err != nil {
		t.Fatal(err)
	}
	account, err := f.accountStore.CreateAccount(t.Context(), "alice", providers.GmailAccountRequest("alice@gmail.test", "alice", "alice-subject"))
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	if err := credentials.UpsertForUser(t.Context(), "alice", account.ID, "google", "alice-subject", "alice-token", "alice-refresh", "Bearer", &expires, "mail"); err != nil {
		t.Fatal(err)
	}
	// The bus drops events for a full subscriber, so collect while syncing.
	events := f.events.Subscribe()
	stop, done := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var seen []mail.Event
	go func() {
		defer close(done)
		for {
			select {
			case e := <-events:
				if e.Type == mail.EventNewMail && e.AccountID == account.ID {
					mu.Lock()
					seen = append(seen, e)
					mu.Unlock()
				}
			case <-stop:
				return
			}
		}
	}()
	t.Cleanup(func() { close(stop); <-done; f.events.Unsubscribe(events) })
	newMail := func() []mail.Event {
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		got := seen
		seen = nil
		return got
	}
	if err := f.imap.Sync(t.Context(), "alice", account.ID); err != nil {
		t.Fatal(err)
	}
	if got := newMail(); len(got) != 0 {
		t.Fatalf("first sync announced mail: %#v", got)
	}
	phase.Store(1)
	if err := f.imap.Sync(t.Context(), "alice", account.ID); err != nil {
		t.Fatal(err)
	}
	got := newMail()
	if len(got) != 1 || got[0].UserID != "alice" || got[0].FolderRole != "inbox" || got[0].Payload["count"] != 1 || got[0].Payload["unread_count"] != 1 || got[0].Payload["subject"] != "Fresh mail" {
		t.Fatalf("history new-mail events = %#v", got)
	}
}
