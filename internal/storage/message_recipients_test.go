package storage

import (
	"context"
	"testing"
	"time"
)

func TestDownloadedRecipientsReplaceSyncedOnes(t *testing.T) {
	ctx := context.Background()
	db := newContactsTestDB(t)
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, user_id, email_address) VALUES ('acc', 'default', 'ana@example.com')`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFolders(ctx, []UpsertFolderInput{{ID: "acc_inbox", AccountID: "acc", RemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertSyncMessages(ctx, []SyncMessage{{
		AccountID: "acc", FolderID: "acc_inbox", RemoteUID: 1, MessageID: "<plans@example.com>", Subject: "Weekend plans",
		FromEmail: "rosa@example.com", DateSent: time.Now(), ToRecipients: []Recipient{{Email: "ana@example.com"}},
	}}); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := db.Read().QueryRowContext(ctx, `SELECT id FROM messages WHERE internet_message_id = '<plans@example.com>'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertRecipientsInternal(ctx, id, []Recipient{{Name: "Ana Ruiz", Email: "ana@example.com"}}, nil); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM message_recipients WHERE message_id = ?`, id).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("recipient rows = %d, %v", rows, err)
	}

	// Messages saved twice by older versions still show each recipient once.
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO message_recipients (message_id, kind, name, email) VALUES (?, 'to', '', 'ANA@example.com')`, id); err != nil {
		t.Fatal(err)
	}
	to, err := db.getRecipients(ctx, id, "to")
	if err != nil || len(to) != 1 || to[0].Name != "Ana Ruiz" {
		t.Fatalf("to = %#v, %v", to, err)
	}
	batch, err := db.batchGetRecipients(ctx, []int64{id}, "to")
	if err != nil || len(batch[id]) != 1 {
		t.Fatalf("batch to = %#v, %v", batch, err)
	}
}
