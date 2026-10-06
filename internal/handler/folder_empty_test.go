package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/storage"
)

func postEmptyFolder(h *Handler, folderID string, asUser func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/folders/"+folderID+"/empty", nil)
	req.SetPathValue("id", folderID)
	rec := httptest.NewRecorder()
	h.handleEmptyFolder(rec, asUser(req))
	return rec
}

func seedEmptyFolders(t *testing.T) (*Handler, *storage.DB) {
	t.Helper()
	h, db := seedUnifiedArchive(t)
	folders := []storage.UpsertFolderInput{
		{ID: "gmail-spam", AccountID: "gmail-acc", RemoteID: "SPAM", ProviderRemoteID: "SPAM", Name: "Spam", Role: "spam", Selectable: true},
		{ID: "outlook-junk", AccountID: "outlook-acc", RemoteID: "Junk", ProviderRemoteID: "graph-junk", Name: "Junk", Role: "junk", Selectable: true},
		{ID: "imap-spam", AccountID: "victim-account", RemoteID: "Spam", Name: "Spam", Role: "spam", Selectable: true},
		{ID: "imap-trash", AccountID: "victim-account", RemoteID: "Trash", Name: "Trash", Role: "trash", Selectable: true},
		{ID: "attacker-spam", AccountID: "attacker-account", RemoteID: "Spam", Name: "Spam", Role: "spam", Selectable: true},
	}
	if err := db.UpsertFolders(t.Context(), folders); err != nil {
		t.Fatal(err)
	}
	for i, f := range folders {
		if err := db.UpsertSyncMessages(t.Context(), []storage.SyncMessage{{
			AccountID: f.AccountID, FolderID: f.ID, RemoteUID: uint32(200 + i),
			MessageID: fmt.Sprintf("<%s@example.com>", f.ID), Subject: "S", FromEmail: "sender@example.com",
			DateSent: time.Now(),
		}}); err != nil {
			t.Fatal(err)
		}
	}
	return h, db
}

func liveCount(t *testing.T, db *storage.DB, folderID string) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRow(`SELECT COUNT(*) FROM message_folder_state WHERE folder_id = ? AND is_deleted = 0`, folderID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func queuedDeletes(t *testing.T, db *storage.DB) map[string]string {
	t.Helper()
	rows, err := db.Read().Query(`SELECT folder_id, provider_type FROM message_mutations WHERE kind = ?`, storage.MessageMutationDelete)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var f, p string
		if err := rows.Scan(&f, &p); err != nil {
			t.Fatal(err)
		}
		out[f] = p
	}
	return out
}

func TestEmptyFolderUnifiedSpamQueuesPermanentDeleteForEachProvider(t *testing.T) {
	h, db := seedEmptyFolders(t)
	rec := postEmptyFolder(h, "spam", ownerRequest)
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"deleted\":3}\n" {
		t.Fatalf("status = %d body = %q, want 200 deleted=3", rec.Code, rec.Body.String())
	}
	want := "map[gmail-spam:gmail imap-spam:imap outlook-junk:outlook]"
	if got := queuedDeletes(t, db); fmt.Sprint(got) != want {
		t.Fatalf("queued deletes = %v, want %s", got, want)
	}
	for _, f := range []string{"gmail-spam", "outlook-junk", "imap-spam"} {
		if n := liveCount(t, db, f); n != 0 {
			t.Errorf("%s still has %d live messages", f, n)
		}
	}
	if liveCount(t, db, "imap-trash") != 1 || liveCount(t, db, "attacker-spam") != 1 || liveCount(t, db, "imap-archive") != 1 {
		t.Fatalf("emptying spam touched trash, another user's spam, or the archive")
	}
}

func TestEmptyFolderDirectTrash(t *testing.T) {
	h, db := seedEmptyFolders(t)
	rec := postEmptyFolder(h, "imap-trash", ownerRequest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
	if got := queuedDeletes(t, db); fmt.Sprint(got) != "map[imap-trash:imap]" {
		t.Fatalf("queued deletes = %v", got)
	}
}

func TestEmptyFolderRejectsForeignMissingAndNonSpamTrashFolders(t *testing.T) {
	h, db := seedEmptyFolders(t)
	foreign := postEmptyFolder(h, "attacker-spam", ownerRequest)
	missing := postEmptyFolder(h, "no-such-folder", ownerRequest)
	if foreign.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign = %d %q, missing = %d %q, want identical 404", foreign.Code, foreign.Body.String(), missing.Code, missing.Body.String())
	}
	for _, id := range []string{"imap-archive", "victim-inbox", "inbox", "archive"} {
		if rec := postEmptyFolder(h, id, ownerRequest); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s status = %d, want 422", id, rec.Code)
		}
	}
	if got := queuedDeletes(t, db); len(got) != 0 {
		t.Fatalf("rejected requests queued deletes: %v", got)
	}
}
