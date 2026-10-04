package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	goimap "github.com/emersion/go-imap/v2"
	"golang.org/x/oauth2"

	mailpkg "github.com/cristianadrielbraun/gofer/internal/mail"
	mailauth "github.com/cristianadrielbraun/gofer/internal/mailauth"
	"github.com/cristianadrielbraun/gofer/internal/models"
	"github.com/cristianadrielbraun/gofer/internal/providers"
	"github.com/cristianadrielbraun/gofer/internal/storage"
)

type fakeFolderReadIMAPClient struct {
	calls    int
	folder   string
	maxUID   uint32
	validity uint32
	flags    []goimap.Flag
	changed  bool
}

func (c *fakeFolderReadIMAPClient) AddFlagsUIDRangeIfUIDValidity(_ context.Context, folder string, maxUID, validity uint32, flags []goimap.Flag) (bool, error) {
	c.calls++
	c.folder, c.maxUID, c.validity, c.flags = folder, maxUID, validity, flags
	return c.changed, nil
}

func (c *fakeFolderReadIMAPClient) Close() error { return nil }

func seedFolderReadHandler(t *testing.T) (*Handler, *storage.DB) {
	t.Helper()
	h, db := newAccountOwnershipTestHandler(t)
	h.syncer = mailpkg.NewSyncOrchestrator(db, nil, nil, nil)
	if _, err := db.Write().ExecContext(t.Context(), `
		INSERT INTO accounts (
			id, user_id, provider, email_address, display_name,
			imap_host, imap_port, imap_tls_mode, smtp_host, smtp_port, smtp_tls_mode, username
		) VALUES ('victim-account-2', 'owner', 'imap', 'owner2@example.com', 'Owner 2',
			'127.0.0.1', 1, 'tls', '127.0.0.1', 1, 'tls', 'owner2@example.com')`); err != nil {
		t.Fatalf("insert second account: %v", err)
	}
	if err := db.UpsertFolders(t.Context(), []storage.UpsertFolderInput{
		{ID: "victim-inbox", AccountID: "victim-account", RemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true},
		{ID: "victim-inbox-2", AccountID: "victim-account-2", RemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true},
		{ID: "attacker-inbox", AccountID: "attacker-account", RemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true},
	}); err != nil {
		t.Fatalf("UpsertFolders() error = %v", err)
	}
	seed := func(account, folder string, uid uint32) {
		if err := db.UpsertSyncMessages(t.Context(), []storage.SyncMessage{{
			AccountID: account, FolderID: folder, RemoteUID: uid,
			MessageID: fmt.Sprintf("<%s-%d@example.com>", folder, uid), Subject: "S", FromEmail: "sender@example.com",
			DateSent: time.Now(), IsRead: false,
		}}); err != nil {
			t.Fatalf("UpsertSyncMessages() error = %v", err)
		}
	}
	seed("victim-account", "victim-inbox", 42)
	seed("victim-account-2", "victim-inbox-2", 7)
	seed("attacker-account", "attacker-inbox", 9)
	return h, db
}

func postFolderRead(h *Handler, folderID string, asUser func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/folders/"+folderID+"/read-all", nil)
	req.SetPathValue("id", folderID)
	rec := httptest.NewRecorder()
	h.handleMarkFolderRead(rec, asUser(req))
	return rec
}

func unreadCount(t *testing.T, db *storage.DB, folderID string) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRow(`SELECT COUNT(*) FROM message_folder_state WHERE folder_id = ? AND is_read = 0`, folderID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFolderReadEndpointOwnerSucceedsAndForeignUserIsRejected(t *testing.T) {
	h, db := seedFolderReadHandler(t)

	rec := postFolderRead(h, "victim-inbox", attackerRequest)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign user status = %d body = %q, want 404", rec.Code, rec.Body.String())
	}
	if unreadCount(t, db, "victim-inbox") != 1 {
		t.Fatalf("foreign request changed the owner's folder")
	}
	var jobs int
	if err := db.Read().QueryRow(`SELECT COUNT(*) FROM folder_read_mutations`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("foreign request queued %d jobs (%v)", jobs, err)
	}
	missing := postFolderRead(h, "no-such-folder", attackerRequest)
	if missing.Code != http.StatusNotFound || missing.Body.String() != rec.Body.String() {
		t.Fatalf("missing folder = %d %q, want response identical to foreign", missing.Code, missing.Body.String())
	}

	rec = postFolderRead(h, "victim-inbox", ownerRequest)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner status = %d body = %q", rec.Code, rec.Body.String())
	}
	var body map[string]int
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["updated"] != 1 {
		t.Fatalf("owner response = %q, %v", rec.Body.String(), err)
	}
	if unreadCount(t, db, "victim-inbox") != 0 || unreadCount(t, db, "attacker-inbox") != 1 {
		t.Fatalf("read state: victim unread=%d attacker unread=%d", unreadCount(t, db, "victim-inbox"), unreadCount(t, db, "attacker-inbox"))
	}
}

func TestFolderReadEndpointUnifiedFolderFansOutToOwnedAccountsOnly(t *testing.T) {
	h, db := seedFolderReadHandler(t)
	rec := postFolderRead(h, "inbox", ownerRequest)
	if rec.Code != http.StatusOK {
		t.Fatalf("unified status = %d body = %q", rec.Code, rec.Body.String())
	}
	if unreadCount(t, db, "victim-inbox") != 0 || unreadCount(t, db, "victim-inbox-2") != 0 {
		t.Fatalf("unified inbox did not cover both owned accounts")
	}
	if unreadCount(t, db, "attacker-inbox") != 1 {
		t.Fatalf("unified inbox touched another user's account")
	}
	var jobs int
	if err := db.Read().QueryRow(`SELECT COUNT(*) FROM folder_read_mutations WHERE folder_id IN ('victim-inbox', 'victim-inbox-2')`).Scan(&jobs); err != nil || jobs != 2 {
		t.Fatalf("queued jobs = %d, %v; want one per account folder", jobs, err)
	}
	if rec := postFolderRead(h, "starred", ownerRequest); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unified starred status = %d, want 422", rec.Code)
	}
}

func TestFolderReadWorkerStoresUIDRangeForIMAP(t *testing.T) {
	h, db := seedFolderReadHandler(t)
	fake := &fakeFolderReadIMAPClient{}
	h.folderReadIMAPFactory = func(context.Context, *models.AccountConfig, string) (folderReadIMAPClient, error) { return fake, nil }
	if _, err := db.Write().Exec(`UPDATE folders SET uid_validity = 555 WHERE id = 'victim-inbox'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MarkFolderReadAndQueueForUser(t.Context(), "owner", "victim-inbox", time.Now()); err != nil {
		t.Fatal(err)
	}
	h.runDueMessageMutations(t.Context())
	if fake.calls != 1 || fake.folder != "INBOX" || fake.maxUID != 42 || fake.validity != 555 || len(fake.flags) != 1 || fake.flags[0] != goimap.FlagSeen {
		t.Fatalf("IMAP range store = %+v; want one \\Seen store for INBOX up to UID 42 at validity 555", fake)
	}
	var jobs int
	if err := db.Read().QueryRow(`SELECT COUNT(*) FROM folder_read_mutations`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("applied job still queued (%d, %v)", jobs, err)
	}

	// UIDVALIDITY change: nothing is stored and the job stays queued for retry.
	fake.changed, fake.calls = true, 0
	if _, err := db.MarkFolderReadAndQueueForUser(t.Context(), "owner", "victim-inbox", time.Now()); err != nil {
		t.Fatal(err)
	}
	h.runDueMessageMutations(t.Context())
	var status string
	if err := db.Read().QueryRow(`SELECT status FROM folder_read_mutations`).Scan(&status); err != nil || status != storage.MessageMutationFailed {
		t.Fatalf("job status after UIDVALIDITY change = %q, %v; want failed", status, err)
	}
}

func TestFolderReadWorkerGmailListsAllPagesAndBatchesByThousand(t *testing.T) {
	ctx := t.Context()
	h, db := newGmailAPITestHandler(t, ctx)
	if err := db.UpsertFolders(ctx, []storage.UpsertFolderInput{{
		ID: "acc_inbox", AccountID: "acc", RemoteID: "INBOX", ProviderRemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true,
	}}); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now()
	var batches []int
	var listQueries []url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/users/me/messages":
			q := r.URL.Query()
			listQueries = append(listQueries, q)
			page, _ := strconv.Atoi(q.Get("pageToken"))
			sizes := []int{500, 500, 200}
			var msgs []map[string]string
			for i := 0; i < sizes[page]; i++ {
				msgs = append(msgs, map[string]string{"id": fmt.Sprintf("m%d-%d", page, i)})
			}
			resp := map[string]any{"messages": msgs}
			if page < 2 {
				resp["nextPageToken"] = strconv.Itoa(page + 1)
			}
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodPost && r.URL.Path == "/users/me/messages/batchModify":
			var payload struct {
				IDs            []string `json:"ids"`
				RemoveLabelIDs []string `json:"removeLabelIds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.RemoveLabelIDs) != 1 || payload.RemoveLabelIDs[0] != "UNREAD" {
				t.Errorf("batchModify payload = %+v, %v", payload, err)
			}
			batches = append(batches, len(payload.IDs))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	previous := gmailAPIBaseURL
	gmailAPIBaseURL = server.URL
	t.Cleanup(func() { gmailAPIBaseURL = previous })

	if _, err := db.MarkFolderReadAndQueueForUser(ctx, "default", "acc_inbox", cutoff); err != nil {
		t.Fatal(err)
	}
	h.runDueMessageMutations(ctx)

	if fmt.Sprint(batches) != "[1000 200]" {
		t.Fatalf("batchModify sizes = %v, want [1000 200]", batches)
	}
	if len(listQueries) != 3 {
		t.Fatalf("list pages = %d, want 3", len(listQueries))
	}
	q := listQueries[0]
	want := "is:unread before:" + strconv.FormatInt(cutoff.Unix(), 10)
	if q.Get("labelIds") != "INBOX" || q.Get("q") != want || q.Get("maxResults") != "500" {
		t.Fatalf("list query = %v, want labelIds=INBOX q=%q maxResults=500", q, want)
	}
	var jobs int
	if err := db.Read().QueryRow(`SELECT COUNT(*) FROM folder_read_mutations`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("applied job still queued (%d, %v)", jobs, err)
	}
}

func TestFolderReadWorkerGmailFailureKeepsJobForRetry(t *testing.T) {
	ctx := t.Context()
	h, db := newGmailAPITestHandler(t, ctx)
	if err := db.UpsertFolders(ctx, []storage.UpsertFolderInput{{
		ID: "acc_inbox", AccountID: "acc", RemoteID: "INBOX", ProviderRemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true,
	}}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	previous := gmailAPIBaseURL
	gmailAPIBaseURL = server.URL
	t.Cleanup(func() { gmailAPIBaseURL = previous })
	if _, err := db.MarkFolderReadAndQueueForUser(ctx, "default", "acc_inbox", time.Now()); err != nil {
		t.Fatal(err)
	}
	h.runDueMessageMutations(ctx)
	var status, lastError string
	if err := db.Read().QueryRow(`SELECT status, last_error FROM folder_read_mutations`).Scan(&status, &lastError); err != nil || status != storage.MessageMutationFailed || lastError == "" {
		t.Fatalf("job = %q %q %v; want failed with error", status, lastError, err)
	}
}

func TestFolderReadWorkerOutlookPagesAndBatchesByTwenty(t *testing.T) {
	ctx := t.Context()
	db, err := storage.New(filepath.Join(t.TempDir(), "gofer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Write().ExecContext(ctx, `INSERT OR IGNORE INTO users (id, username, username_normalized, name) VALUES ('default', 'default', 'default', 'Default')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().ExecContext(ctx, `
		INSERT INTO accounts (id, user_id, provider, provider_account_id, email_address)
		VALUES ('acc', 'default', ?, 'subject-id', 'user@example.com')`, providers.ProviderOutlook); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFolders(ctx, []storage.UpsertFolderInput{{
		ID: "acc_inbox", AccountID: "acc", RemoteID: "INBOX", ProviderRemoteID: "graph-inbox", Name: "Inbox", Role: "inbox", Selectable: true,
	}}); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	manager := mailauth.New(&mailauth.Config{MicrosoftClient: &oauth2.Config{}}, db, testMailboxCredentialKey)
	if err := manager.UpsertOAuthAccount(ctx, "acc", providers.OAuthMicrosoft, "subject-id", "stale", "refresh-token", "Bearer", &expires, ""); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2026, 10, 4, 12, 30, 0, 0, time.UTC)
	var batchSizes []int
	var listFilters []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"graph-token","refresh_token":"refresh-token","token_type":"Bearer","expires_in":3600}`))
		case r.Method == http.MethodGet && r.URL.Path == "/me/mailFolders/graph-inbox/messages":
			listFilters = append(listFilters, r.URL.Query().Get("$filter"))
			var value []map[string]string
			for i := 0; i < 30; i++ {
				value = append(value, map[string]string{"id": fmt.Sprintf("a%d", i)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"value": value, "@odata.nextLink": server.URL + "/page2"})
		case r.Method == http.MethodGet && r.URL.Path == "/page2":
			var value []map[string]string
			for i := 0; i < 15; i++ {
				value = append(value, map[string]string{"id": fmt.Sprintf("b%d", i)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"value": value})
		case r.Method == http.MethodPost && r.URL.Path == "/$batch":
			var payload struct {
				Requests []struct {
					ID, Method, URL string
					Body            map[string]bool
				} `json:"requests"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode batch: %v", err)
			}
			batchSizes = append(batchSizes, len(payload.Requests))
			var responses []map[string]any
			for _, req := range payload.Requests {
				if req.Method != http.MethodPatch || !strings.HasPrefix(req.URL, "/me/messages/") || !req.Body["isRead"] {
					t.Errorf("batch item = %+v, want PATCH /me/messages/{id} isRead=true", req)
				}
				responses = append(responses, map[string]any{"id": req.ID, "status": 200})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"responses": responses})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	h := &Handler{db: db, mailboxAuth: mailauth.New(&mailauth.Config{MicrosoftClient: &oauth2.Config{Endpoint: oauth2.Endpoint{TokenURL: server.URL + "/token"}}}, db, testMailboxCredentialKey)}
	previous := outlookGraphBaseURL
	outlookGraphBaseURL = server.URL
	t.Cleanup(func() { outlookGraphBaseURL = previous })

	if _, err := db.MarkFolderReadAndQueueForUser(ctx, "default", "acc_inbox", cutoff); err != nil {
		t.Fatal(err)
	}
	h.runDueMessageMutations(ctx)

	if fmt.Sprint(batchSizes) != "[20 20 5]" {
		t.Fatalf("$batch sizes = %v, want [20 20 5] (45 ids over 2 pages)", batchSizes)
	}
	if len(listFilters) != 1 || listFilters[0] != "isRead eq false and receivedDateTime le 2026-10-04T12:30:00Z" {
		t.Fatalf("list filters = %v", listFilters)
	}
	var jobs int
	if err := db.Read().QueryRow(`SELECT COUNT(*) FROM folder_read_mutations`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("applied job still queued (%d, %v)", jobs, err)
	}
}
