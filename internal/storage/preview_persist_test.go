package storage

import (
	"context"
	"github.com/cristianadrielbraun/gofer/internal/models"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmptySnippetPreviewIsPersistedOnFirstRender(t *testing.T) {
	ctx := context.Background()
	db := newContactsTestDB(t)
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, user_id, provider, email_address) VALUES ('acc', 'default', 'imap', 'u@example.com')`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFolders(ctx, []UpsertFolderInput{{ID: "inbox", AccountID: "acc", RemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertSyncMessages(ctx, []SyncMessage{{AccountID: "acc", FolderID: "inbox", RemoteUID: 1, MessageID: "<p@x>", Subject: "Hello",
		FromEmail: "f@x.com", DateSent: time.Now(), Snippet: ""}}); err != nil {
		t.Fatal(err)
	}
	body := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(body, []byte("The real first line of the body"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().ExecContext(ctx, `UPDATE messages SET body_text_path = ?`, body); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RefreshFolderUnreadCount(ctx, "inbox"); err != nil {
		t.Fatal(err)
	}

	page, err := db.GetEmailsRangeFiltered(ctx, "inbox", 0, 10, models.EmailFilters{})
	if err != nil || len(page.Emails) != 1 {
		t.Fatalf("page = %+v, %v", page, err)
	}
	if got := page.Emails[0].Preview; got != "The real first line of the body" {
		t.Fatalf("first render preview = %q", got)
	}
	var stored string
	if err := db.Read().QueryRow(`SELECT preview_text FROM messages`).Scan(&stored); err != nil || stored != "The real first line of the body" {
		t.Fatalf("preview_text = %q, %v", stored, err)
	}

	// Second render must not need the file.
	if err := os.Remove(body); err != nil {
		t.Fatal(err)
	}
	page, err = db.GetEmailsRangeFiltered(ctx, "inbox", 0, 10, models.EmailFilters{})
	if err != nil || len(page.Emails) != 1 || page.Emails[0].Preview != "The real first line of the body" {
		t.Fatalf("second render = %+v, %v", page, err)
	}
}

func TestPreviewReadIsCapped(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "ok.txt")
	late := filepath.Join(dir, "late.txt")
	if err := os.WriteFile(ok, []byte("early words"+strings.Repeat(" ", 1<<20)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(late, []byte(strings.Repeat(" ", previewMaxTextBytes+10)+"too late"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := previewFromBodyPaths(ok, ""); got != "early words" {
		t.Fatalf("early preview = %q", got)
	}
	if got := previewFromBodyPaths(late, ""); got != "" {
		t.Fatalf("text past the cap should not be read, got %q", got)
	}
}
