package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func seedFolderReadTest(t *testing.T, provider string, unread int) *DB {
	t.Helper()
	ctx := context.Background()
	db := newContactsTestDB(t)
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, user_id, provider, email_address) VALUES ('acc', 'default', ?, 'user@example.com')`, provider); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if err := db.UpsertFolders(ctx, []UpsertFolderInput{{
		ID: "f-inbox", AccountID: "acc", RemoteID: "INBOX", ProviderRemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true,
	}}); err != nil {
		t.Fatalf("UpsertFolders() error = %v", err)
	}
	for i := 1; i <= unread; i++ {
		seedFolderReadMessage(t, db, i, false)
	}
	return db
}

func seedFolderReadMessage(t *testing.T, db *DB, n int, read bool) {
	t.Helper()
	if err := db.UpsertSyncMessages(context.Background(), []SyncMessage{{
		AccountID: "acc", FolderID: "f-inbox", RemoteUID: uint32(n),
		MessageID: fmt.Sprintf("<m%d@example.com>", n), Subject: "Subject", FromEmail: "sender@example.com",
		DateSent: time.Now(), IsRead: read,
	}}); err != nil {
		t.Fatalf("UpsertSyncMessages(%d) error = %v", n, err)
	}
}

func folderReadState(t *testing.T, db *DB, n int) bool {
	t.Helper()
	var read int
	if err := db.Read().QueryRow(`
		SELECT mfs.is_read FROM message_folder_state mfs JOIN messages m ON m.id = mfs.message_id
		WHERE m.internet_message_id = ? AND mfs.folder_id = 'f-inbox'`, fmt.Sprintf("<m%d@example.com>", n)).Scan(&read); err != nil {
		t.Fatalf("read state %d: %v", n, err)
	}
	return read == 1
}

func TestMarkFolderReadCutoffKeepsNewerMailUnreadAndQueuesOneJob(t *testing.T) {
	for _, provider := range []string{"imap", "gmail", "outlook"} {
		t.Run(provider, func(t *testing.T) {
			db := seedFolderReadTest(t, provider, 3)
			ctx := context.Background()
			results, err := db.MarkFolderReadAndQueueForUser(ctx, "default", "f-inbox", time.Now())
			if err != nil || len(results) != 1 || results[0].Marked != 3 {
				t.Fatalf("MarkFolderReadAndQueueForUser() = %+v, %v; want 3 marked", results, err)
			}
			for n := 1; n <= 3; n++ {
				if !folderReadState(t, db, n) {
					t.Fatalf("message %d still unread locally", n)
				}
			}
			var mutations int
			if err := db.Read().QueryRow(`SELECT COUNT(*) FROM message_mutations`).Scan(&mutations); err != nil || mutations != 0 {
				t.Fatalf("per-message mutations = %d, %v; want none", mutations, err)
			}
			var jobs, cutoffID, maxUID int
			if err := db.Read().QueryRow(`SELECT COUNT(*), MAX(cutoff_message_id), MAX(max_uid) FROM folder_read_mutations`).Scan(&jobs, &cutoffID, &maxUID); err != nil || jobs != 1 {
				t.Fatalf("folder jobs = %d, %v; want exactly one", jobs, err)
			}
			var maxMessageID int
			if err := db.Read().QueryRow(`SELECT MAX(id) FROM messages`).Scan(&maxMessageID); err != nil || cutoffID != maxMessageID {
				t.Fatalf("cutoff_message_id = %d, want %d (%v)", cutoffID, maxMessageID, err)
			}
			if provider == "imap" && maxUID != 3 {
				t.Fatalf("max_uid = %d, want 3", maxUID)
			}

			// Mail that arrives after the request stays unread, and a stale sync
			// of an older message cannot flip it back before the provider call lands.
			seedFolderReadMessage(t, db, 4, false)
			seedFolderReadMessage(t, db, 1, false)
			if folderReadState(t, db, 4) {
				t.Fatalf("message newer than the cutoff was marked read")
			}
			if !folderReadState(t, db, 1) {
				t.Fatalf("stale sync reverted a message covered by a queued folder read")
			}

			// Re-requesting re-arms the single job instead of adding another.
			if _, err := db.MarkFolderReadAndQueueForUser(ctx, "default", "f-inbox", time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := db.Read().QueryRow(`SELECT COUNT(*) FROM folder_read_mutations`).Scan(&jobs); err != nil || jobs != 1 {
				t.Fatalf("jobs after re-request = %d, %v", jobs, err)
			}
			if !folderReadState(t, db, 4) {
				t.Fatalf("second request did not mark message 4")
			}
		})
	}
}

func TestMarkFolderReadSupersedesPendingPerMessageReadMutation(t *testing.T) {
	db := seedFolderReadTest(t, "imap", 2)
	ctx := context.Background()
	var messageID int64
	if err := db.Read().QueryRow(`SELECT id FROM messages WHERE internet_message_id = '<m1@example.com>'`).Scan(&messageID); err != nil {
		t.Fatal(err)
	}
	// The user marked the message unread; that read=false mutation is still pending.
	if err := db.SetMessageReadAndQueue(ctx, messageID, false); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err := db.Read().QueryRow(`SELECT COUNT(*) FROM message_mutations WHERE kind = 'read' AND status = 'pending'`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("pending read mutations = %d, %v; want 1 before", pending, err)
	}
	if _, err := db.MarkFolderReadAndQueueForUser(ctx, "default", "f-inbox", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.Read().QueryRow(`SELECT COUNT(*) FROM message_mutations WHERE message_id = ?`, messageID).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("read mutations after folder read = %d, %v; want superseded", pending, err)
	}
	// The worker would otherwise send read=false after the folder-wide read.
	claimed, err := db.ClaimDueMessageMutations(ctx, time.Now().Add(time.Minute), 10)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("claimed per-message mutations = %d, %v; want none", len(claimed), err)
	}
	jobs, err := db.ClaimDueFolderReadMutations(ctx, time.Now().Add(time.Minute), 10)
	if err != nil || len(jobs) != 1 || jobs[0].FolderID != "f-inbox" || jobs[0].Status != MessageMutationPending {
		t.Fatalf("claimed folder jobs = %+v, %v", jobs, err)
	}
}

func TestMarkFolderReadRejectsForeignUserAndUnsupportedFolders(t *testing.T) {
	db := seedFolderReadTest(t, "gmail", 1)
	ctx := context.Background()
	if _, err := db.MarkFolderReadAndQueueForUser(ctx, "someone-else", "f-inbox", time.Now()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign user err = %v, want sql.ErrNoRows", err)
	}
	if err := db.UpsertFolders(ctx, []UpsertFolderInput{{
		ID: "gmail-archive", AccountID: "acc", RemoteID: "ARCHIVE", ProviderRemoteID: "ARCHIVE", Name: "Archive", Role: "archive", Selectable: true,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MarkFolderReadAndQueueForUser(ctx, "default", "gmail-archive", time.Now()); !errors.Is(err, ErrFolderReadUnsupported) {
		t.Fatalf("Gmail archive err = %v, want ErrFolderReadUnsupported", err)
	}
	if _, err := db.MarkFolderReadAndQueueForUser(ctx, "default", "starred", time.Now()); !errors.Is(err, ErrFolderReadUnsupported) {
		t.Fatalf("starred err = %v, want ErrFolderReadUnsupported", err)
	}
	var unread int
	if err := db.Read().QueryRow(`SELECT COUNT(*) FROM message_folder_state WHERE is_read = 0`).Scan(&unread); err != nil || unread != 1 {
		t.Fatalf("rejected requests changed state: unread=%d err=%v", unread, err)
	}
}

func TestMigrateV94ToV95AddsFolderReadQueue(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gofer.db")
	raw, err := openDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP);
		INSERT INTO schema_version (version) VALUES (94);
		CREATE TABLE accounts (id TEXT PRIMARY KEY, email_address TEXT NOT NULL DEFAULT '', label TEXT NOT NULL DEFAULT '');
		CREATE TABLE folders (id TEXT PRIMARY KEY)`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	_ = raw.Close()
	db, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var version, tables int
	if err := db.Read().QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil || version != CurrentSchemaVersion {
		t.Fatalf("schema version = %d, %v; want %d", version, err, CurrentSchemaVersion)
	}
	if err := db.Read().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'folder_read_mutations'`).Scan(&tables); err != nil || tables != 1 {
		t.Fatalf("folder_read_mutations tables = %d, %v", tables, err)
	}
}
