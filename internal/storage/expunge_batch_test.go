package storage

import (
	"context"
	"testing"
)

// seedBulkInbox inserts n single-message threads into one folder with
// remote_uid 1..n, bypassing the sync path so a SQLite-variable-limit sized
// mailbox seeds in well under a second.
func seedBulkInbox(t *testing.T, n int) *DB {
	t.Helper()
	ctx := context.Background()
	db := newContactsTestDB(t)
	if _, err := db.Write().ExecContext(ctx, `
		INSERT INTO accounts (id, user_id, provider, email_address) VALUES ('acc', 'default', 'imap', 'u@example.com');
		INSERT INTO folders (id, account_id, remote_id, name, role) VALUES ('inbox', 'acc', 'INBOX', 'Inbox', 'inbox');`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().ExecContext(ctx, `
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < ?)
		INSERT INTO messages (id, account_id, internet_message_id, thread_id, subject, date_received)
		SELECT i, 'acc', '<m' || i || '@x>', 't' || i, 's', '2026-01-01 10:00:00' FROM n`, n); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().ExecContext(ctx, `
		INSERT INTO message_folder_state (message_id, folder_id, remote_uid, is_read, synced_at)
		SELECT id, 'inbox', id, 1, CURRENT_TIMESTAMP FROM messages`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestRemoveExpungedUIDsHandlesMoreThanSQLiteVariableLimit(t *testing.T) {
	ctx := context.Background()
	const total = 33000 // above SQLite's 32766 bound-variable limit
	db := seedBulkInbox(t, total)
	if err := db.RefreshFolderThreadState(ctx, "inbox"); err != nil {
		t.Fatal(err)
	}
	uids := make([]uint32, 0, total-1)
	for uid := uint32(2); uid <= total; uid++ { // keep UID 1
		uids = append(uids, uid)
	}
	removed, err := db.RemoveExpungedUIDs(ctx, "inbox", uids)
	if err != nil {
		t.Fatalf("RemoveExpungedUIDs() error = %v", err)
	}
	if removed != total-1 {
		t.Fatalf("removed = %d, want %d", removed, total-1)
	}
	var messages, states, threads int
	if err := db.Read().QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM messages), (SELECT COUNT(*) FROM message_folder_state), (SELECT COUNT(*) FROM folder_thread_state)`).Scan(&messages, &states, &threads); err != nil {
		t.Fatal(err)
	}
	if messages != 1 || states != 1 || threads != 1 {
		t.Fatalf("left messages=%d states=%d threads=%d, want 1 each", messages, states, threads)
	}
}
