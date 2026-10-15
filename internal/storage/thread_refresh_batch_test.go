package storage

import (
	"context"
	"testing"
)

func TestRefreshFolderThreadsForMessagesHandlesMoreIDsThanSQLiteVariableLimit(t *testing.T) {
	ctx := context.Background()
	const total = 17000 // ids are bound twice, so this exceeds SQLite's 32766 limit
	db := seedBulkInbox(t, total)
	ids := make([]int64, total)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	if err := db.RefreshFolderThreadsForMessages(ctx, "inbox", ids); err != nil {
		t.Fatalf("RefreshFolderThreadsForMessages() error = %v", err)
	}
	var threads int
	if err := db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM folder_thread_state WHERE folder_id = 'inbox'`).Scan(&threads); err != nil {
		t.Fatal(err)
	}
	if threads != total {
		t.Fatalf("folder_thread_state rows = %d, want %d", threads, total)
	}
}
