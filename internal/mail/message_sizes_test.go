package mail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/storage"
)

func TestOutlookMessageSizesComeFromTheSizeProperty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/$batch" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Requests []providerBatchRequest `json:"requests"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Requests) != 3 {
			t.Errorf("batch = %#v, %v", body, err)
		}
		if !strings.Contains(body.Requests[0].URL, "singleValueExtendedProperties") || !strings.Contains(body.Requests[0].URL, "0x0E08") {
			t.Errorf("request URL = %s", body.Requests[0].URL)
		}
		_, _ = w.Write([]byte(`{"responses":[
			{"id":"0","status":200,"body":{"id":"m-1","singleValueExtendedProperties":[{"id":"Integer 0xe08","value":"52341"}]}},
			{"id":"1","status":404,"body":{"error":{"code":"ErrorItemNotFound"}}},
			{"id":"2","status":200,"body":{"id":"m-3"}}
		]}`))
	}))
	defer server.Close()
	previous := outlookGraphBaseURL
	outlookGraphBaseURL = server.URL
	t.Cleanup(func() { outlookGraphBaseURL = previous })

	sizes := map[string]int64{}
	if err := fetchOutlookMessageSizes(context.Background(), "token", []string{"m-1", "m-2", "m-3"}, sizes); err != nil {
		t.Fatal(err)
	}
	if sizes["m-1"] != 52341 || sizes["m-2"] != storage.MessageSizeUnavailable || sizes["m-3"] != storage.MessageSizeUnavailable {
		t.Fatalf("sizes = %v", sizes)
	}
}

func TestOutlookMessageSizesStopWhenThrottled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"responses":[{"id":"0","status":429,"body":{}}]}`))
	}))
	defer server.Close()
	previous := outlookGraphBaseURL
	outlookGraphBaseURL = server.URL
	t.Cleanup(func() { outlookGraphBaseURL = previous })

	sizes := map[string]int64{}
	if err := fetchOutlookMessageSizes(context.Background(), "token", []string{"m-1"}, sizes); err == nil || len(sizes) != 0 {
		t.Fatalf("throttled lookup = %v, %v", sizes, err)
	}
}

func TestGmailMessageSizeAsksOnlyForTheSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/me/messages/g-1" || r.URL.Query().Get("format") != "minimal" || r.URL.Query().Get("fields") != "id,sizeEstimate" {
			t.Errorf("request = %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"id":"g-1","sizeEstimate":81234}`))
	}))
	defer server.Close()
	previous := gmailAPIBaseURL
	gmailAPIBaseURL = server.URL
	t.Cleanup(func() { gmailAPIBaseURL = previous })

	size, err := getGmailAPIMessageSize(context.Background(), "token", "g-1")
	if err != nil || size != 81234 {
		t.Fatalf("size = %d, %v", size, err)
	}
}

func TestGmailSyncKeepsTheSizeEstimate(t *testing.T) {
	syncs := gmailAPIMessageToProviderSyncs("acc", gmailAPIMessage{ID: "g-1", LabelIDs: []string{"INBOX"}, SizeEstimate: 4321}, nil, map[string]gmailAPIFolderSyncTarget{
		"INBOX": {Folder: storage.FolderSyncInfo{ID: "acc_inbox"}, LabelID: "INBOX"},
	})
	if len(syncs) != 1 || syncs[0].SizeBytes != 4321 {
		t.Fatalf("syncs = %#v", syncs)
	}
}

func seedProviderMessagesMissingSize(t *testing.T, provider string, count int) *storage.DB {
	t.Helper()
	ctx := context.Background()
	db := newLabelSyncTestDB(t)
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, user_id, provider, email_address) VALUES ('acc', 'default', ?, 'ana@example.com')`, provider); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFolders(ctx, []storage.UpsertFolderInput{{ID: "acc_inbox", AccountID: "acc", RemoteID: "INBOX", ProviderRemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true}}); err != nil {
		t.Fatal(err)
	}
	var msgs []storage.ProviderSyncMessage
	for i := range count {
		msgs = append(msgs, storage.ProviderSyncMessage{AccountID: "acc", FolderID: "acc_inbox", ProviderMessageID: "m-" + strconv.Itoa(i),
			InternetMessageID: "<note-" + strconv.Itoa(i) + "@example.com>", Subject: "Note", DateSent: time.Now()})
	}
	if _, err := db.UpsertProviderSyncMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	return db
}

func storedSizes(t *testing.T, db *storage.DB) map[string]int64 {
	t.Helper()
	rows, err := db.Read().Query(`SELECT remote_message_id, size_bytes FROM messages`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	sizes := map[string]int64{}
	for rows.Next() {
		var id string
		var size int64
		if err := rows.Scan(&id, &size); err != nil {
			t.Fatal(err)
		}
		sizes[id] = size
	}
	return sizes
}

func TestGmailSizeLookupRunsInParallelAndSavesWhatItFound(t *testing.T) {
	db := seedProviderMessagesMissingSize(t, "gmail", 12)
	var active, most atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := active.Add(1)
		defer active.Add(-1)
		for {
			prev := most.Load()
			if now <= prev || most.CompareAndSwap(prev, now) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		id := strings.TrimPrefix(r.URL.Path, "/users/me/messages/")
		if id == "m-7" {
			http.Error(w, `{"error":{"code":404,"message":"Requested entity was not found."}}`, http.StatusNotFound)
			return
		}
		n, _ := strconv.Atoi(strings.TrimPrefix(id, "m-"))
		_, _ = w.Write([]byte(`{"id":"` + id + `","sizeEstimate":` + strconv.Itoa(1000+n) + `}`))
	}))
	defer server.Close()
	previous := gmailAPIBaseURL
	gmailAPIBaseURL = server.URL
	t.Cleanup(func() { gmailAPIBaseURL = previous })

	NewSyncOrchestrator(db, nil, nil, labelSyncTestTokens{}).backfillGmailMessageSizes(context.Background(), "acc")
	sizes := storedSizes(t, db)
	for i := range 12 {
		id := "m-" + strconv.Itoa(i)
		want := int64(1000 + i)
		if i == 7 {
			want = storage.MessageSizeUnavailable
		}
		if sizes[id] != want {
			t.Fatalf("size of %s = %d, want %d", id, sizes[id], want)
		}
	}
	if most.Load() < 2 {
		t.Fatalf("lookups ran one at a time")
	}
}

func TestGmailSizeLookupStopsAtTheFirstFailure(t *testing.T) {
	db := seedProviderMessagesMissingSize(t, "gmail", 40)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, `{"error":{"code":403,"message":"Quota exceeded","errors":[{"reason":"rateLimitExceeded"}]}}`, http.StatusForbidden)
	}))
	defer server.Close()
	previous := gmailAPIBaseURL
	gmailAPIBaseURL = server.URL
	t.Cleanup(func() { gmailAPIBaseURL = previous })

	t.Cleanup(func() { sizeLookupsPausedUntil.Delete("acc") })
	orchestrator := NewSyncOrchestrator(db, nil, nil, labelSyncTestTokens{})
	orchestrator.backfillGmailMessageSizes(context.Background(), "acc")
	for id, size := range storedSizes(t, db) {
		if size != 0 {
			t.Fatalf("size of %s = %d after a rate limit, want it left for the next sync", id, size)
		}
	}
	// The next sync doesn't ask again while the pause lasts.
	before := requests.Load()
	orchestrator.backfillGmailMessageSizes(context.Background(), "acc")
	if requests.Load() != before {
		t.Fatal("size lookups retried right after a rate limit")
	}
}

func TestOutlookSizeLookupBatchesRequests(t *testing.T) {
	db := seedProviderMessagesMissingSize(t, "outlook", 45)
	var batches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		batches.Add(1)
		var body struct {
			Requests []providerBatchRequest `json:"requests"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var responses []string
		for _, req := range body.Requests {
			id := strings.TrimPrefix(strings.SplitN(req.URL, "?", 2)[0], "/me/messages/")
			n, _ := strconv.Atoi(strings.TrimPrefix(id, "m-"))
			responses = append(responses, `{"id":"`+req.ID+`","status":200,"body":{"id":"`+id+`","singleValueExtendedProperties":[{"id":"Integer 0xe08","value":"`+strconv.Itoa(2000+n)+`"}]}}`)
		}
		_, _ = w.Write([]byte(`{"responses":[` + strings.Join(responses, ",") + `]}`))
	}))
	defer server.Close()
	previous := outlookGraphBaseURL
	outlookGraphBaseURL = server.URL
	t.Cleanup(func() { outlookGraphBaseURL = previous })

	NewSyncOrchestrator(db, nil, nil, labelSyncTestTokens{}).backfillOutlookMessageSizes(context.Background(), "acc")
	if batches.Load() != 3 {
		t.Fatalf("batch requests = %d, want 3 for 45 messages", batches.Load())
	}
	for id, size := range storedSizes(t, db) {
		n, _ := strconv.Atoi(strings.TrimPrefix(id, "m-"))
		if size != int64(2000+n) {
			t.Fatalf("size of %s = %d", id, size)
		}
	}
}
