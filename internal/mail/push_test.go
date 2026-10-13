package mail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/providers"
)

func newPushTestOrchestrator(t *testing.T, accounts map[string][2]string) *SyncOrchestrator {
	t.Helper()
	db := newLabelSyncTestDB(t)
	for id, providerAndEmail := range accounts {
		if _, err := db.Write().Exec(`INSERT INTO accounts (id, user_id, provider, email_address) VALUES (?, 'default', ?, ?)`, id, providerAndEmail[0], providerAndEmail[1]); err != nil {
			t.Fatalf("insert account %s: %v", id, err)
		}
	}
	o := NewSyncOrchestrator(db, nil, nil, labelSyncTestTokens{})
	o.push = PushConfig{Secret: "s3cret", GmailTopic: "projects/p/topics/raven-gmail", BaseURL: "https://raven.example"}
	return o
}

func gmailPushBody(t *testing.T, data string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{"message": map[string]string{"data": data}, "subscription": "projects/p/subscriptions/s"})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestPushSecretValidRejectsEmptySecret(t *testing.T) {
	o := NewSyncOrchestrator(newLabelSyncTestDB(t), nil, nil, nil)
	if o.PushSecretValid("") || o.PushEnabled() {
		t.Fatal("push must be off, and reject an empty token, when no secret is configured")
	}
	o.push.Secret = "s3cret"
	if !o.PushSecretValid("s3cret") || o.PushSecretValid("s3cre") || o.PushSecretValid("") {
		t.Fatal("PushSecretValid must accept only the exact secret")
	}
}

func TestHandleGmailPushSyncsOnlyMatchingGmailAccounts(t *testing.T) {
	o := newPushTestOrchestrator(t, map[string][2]string{
		"acc":   {providers.ProviderGmail, " User@Example.com "},
		"other": {providers.ProviderGmail, "other@example.com"},
		"ol":    {providers.ProviderOutlook, "user@example.com"},
	})
	var mu sync.Mutex
	var synced []string
	o.syncAccountOverride = func(_ context.Context, accountID string, _ bool) error {
		mu.Lock()
		synced = append(synced, accountID)
		mu.Unlock()
		return nil
	}

	data := base64.StdEncoding.EncodeToString([]byte(`{"emailAddress":"user@example.com","historyId":1234}`))
	o.HandleGmailPush(context.Background(), gmailPushBody(t, data))
	// Malformed payloads and unknown addresses must be ignored without a sync.
	o.HandleGmailPush(context.Background(), []byte("not json"))
	o.HandleGmailPush(context.Background(), gmailPushBody(t, "!!!not-base64!!!"))
	o.HandleGmailPush(context.Background(), gmailPushBody(t, base64.StdEncoding.EncodeToString([]byte(`{"emailAddress":"nobody@example.com"}`))))

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(synced)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(synced) != 1 || synced[0] != "acc" {
		t.Fatalf("synced accounts = %v, want [acc]", synced)
	}
}

func TestHandleGraphPushOnlySyncsGraphAccounts(t *testing.T) {
	o := newPushTestOrchestrator(t, map[string][2]string{
		"ol":    {providers.ProviderOutlook, "a@example.com"},
		"gmail": {providers.ProviderGmail, "b@example.com"},
	})
	synced := make(chan string, 4)
	o.syncAccountOverride = func(_ context.Context, accountID string, _ bool) error {
		synced <- accountID
		return nil
	}
	o.HandleGraphPush(context.Background(), "gmail", false)
	o.HandleGraphPush(context.Background(), "missing", false)
	o.HandleGraphPush(context.Background(), "ol", false)
	select {
	case got := <-synced:
		if got != "ol" {
			t.Fatalf("synced %q, want ol", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("graph account was not synced")
	}
	select {
	case got := <-synced:
		t.Fatalf("unexpected extra sync for %q", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRenewPushWatchStartsGmailWatch(t *testing.T) {
	o := newPushTestOrchestrator(t, map[string][2]string{"acc": {providers.ProviderGmail, "user@example.com"}})
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/users/me/watch" {
			t.Errorf("unexpected Gmail request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer gmail-token" {
			t.Errorf("Authorization = %q", got)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"historyId":"1","expiration":"1893456000000"}`))
	}))
	defer server.Close()
	previous := gmailAPIBaseURL
	gmailAPIBaseURL = server.URL
	t.Cleanup(func() { gmailAPIBaseURL = previous })

	o.renewPushWatch(context.Background(), "acc")

	labels, _ := gotBody["labelIds"].([]any)
	if gotBody["topicName"] != "projects/p/topics/raven-gmail" || len(labels) != 1 || labels[0] != "INBOX" || gotBody["labelFilterBehavior"] != "include" {
		t.Fatalf("watch body = %#v", gotBody)
	}
}

func TestRenewPushWatchSkipsGmailWithoutTopic(t *testing.T) {
	o := newPushTestOrchestrator(t, map[string][2]string{"acc": {providers.ProviderGmail, "user@example.com"}})
	o.push.GmailTopic = ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	previous := gmailAPIBaseURL
	gmailAPIBaseURL = server.URL
	t.Cleanup(func() { gmailAPIBaseURL = previous })
	o.renewPushWatch(context.Background(), "acc")
}

type graphSubscriptionCall struct {
	Method string
	Path   string
	Body   map[string]any
}

func fakeGraphSubscriptions(t *testing.T, existing []map[string]string) (*[]graphSubscriptionCall, func()) {
	t.Helper()
	var mu sync.Mutex
	var calls []graphSubscriptionCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := graphSubscriptionCall{Method: r.Method, Path: r.URL.Path}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&call.Body)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer graph-token" {
			t.Errorf("Authorization = %q", got)
		}
		mu.Lock()
		calls = append(calls, call)
		mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/subscriptions":
			_ = json.NewEncoder(w).Encode(map[string]any{"value": existing})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	previous := outlookGraphBaseURL
	outlookGraphBaseURL = server.URL
	return &calls, func() { outlookGraphBaseURL = previous; server.Close() }
}

func TestRenewPushWatchCreatesGraphSubscriptionWhenNoneExists(t *testing.T) {
	o := newPushTestOrchestrator(t, map[string][2]string{"acc/1": {providers.ProviderOutlook, "user@example.com"}})
	calls, restore := fakeGraphSubscriptions(t, []map[string]string{{"id": "unrelated", "notificationUrl": "https://elsewhere.example/hook"}})
	defer restore()

	o.renewPushWatch(context.Background(), "acc/1")

	if len(*calls) != 2 || (*calls)[0].Method != http.MethodGet || (*calls)[1].Method != http.MethodPost || (*calls)[1].Path != "/subscriptions" {
		t.Fatalf("calls = %#v, want GET then POST /subscriptions", *calls)
	}
	body := (*calls)[1].Body
	wantURL := "https://raven.example/hooks/graph?account=" + url.QueryEscape("acc/1")
	if body["notificationUrl"] != wantURL || body["lifecycleNotificationUrl"] != wantURL || body["clientState"] != "s3cret" ||
		body["changeType"] != "created" || body["resource"] != "me/mailFolders('inbox')/messages" {
		t.Fatalf("create body = %#v", body)
	}
	expires, err := time.Parse(time.RFC3339, body["expirationDateTime"].(string))
	if err != nil {
		t.Fatalf("expirationDateTime: %v", err)
	}
	if ttl := time.Until(expires); ttl <= 48*time.Hour || ttl >= 4230*time.Minute {
		t.Fatalf("subscription ttl = %v, want under Graph's 4230 minute cap", ttl)
	}
}

func TestRenewPushWatchPatchesExistingGraphSubscriptionAndDeletesDuplicates(t *testing.T) {
	o := newPushTestOrchestrator(t, map[string][2]string{"acc": {providers.ProviderOutlook, "user@example.com"}})
	ours := "https://raven.example/hooks/graph?account=acc"
	calls, restore := fakeGraphSubscriptions(t, []map[string]string{
		{"id": "sub-1", "notificationUrl": ours},
		{"id": "sub-other", "notificationUrl": "https://elsewhere.example/hook"},
		{"id": "sub-2", "notificationUrl": ours},
	})
	defer restore()

	o.renewPushWatch(context.Background(), "acc")

	if len(*calls) != 3 {
		t.Fatalf("calls = %#v, want GET, PATCH, DELETE", *calls)
	}
	if c := (*calls)[1]; c.Method != http.MethodPatch || c.Path != "/subscriptions/sub-1" || c.Body["expirationDateTime"] == nil {
		t.Fatalf("second call = %#v, want PATCH /subscriptions/sub-1 with expirationDateTime", c)
	}
	if c := (*calls)[2]; c.Method != http.MethodDelete || c.Path != "/subscriptions/sub-2" {
		t.Fatalf("third call = %#v, want DELETE /subscriptions/sub-2", c)
	}
}
