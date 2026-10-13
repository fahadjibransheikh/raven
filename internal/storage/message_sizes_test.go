package storage

import (
	"context"
	"testing"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

func TestMessageSizesAreStoredAndFiltered(t *testing.T) {
	ctx := context.Background()
	db := newContactsTestDB(t)
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, user_id, email_address) VALUES ('acc', 'default', 'ana@example.com')`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFolders(ctx, []UpsertFolderInput{{ID: "acc_inbox", AccountID: "acc", RemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	msgs := []SyncMessage{
		{AccountID: "acc", FolderID: "acc_inbox", RemoteUID: 1, MessageID: "<small@example.com>", Subject: "Lunch", FromEmail: "rosa@example.com", DateSent: now, SizeBytes: 2 << 10},
		{AccountID: "acc", FolderID: "acc_inbox", RemoteUID: 2, MessageID: "<large@example.com>", Subject: "Site photos", FromEmail: "rosa@example.com", DateSent: now, SizeBytes: 3 << 20},
		{AccountID: "acc", FolderID: "acc_inbox", RemoteUID: 3, MessageID: "<old@example.com>", Subject: "Old note", FromEmail: "rosa@example.com", DateSent: now},
	}
	if err := db.UpsertSyncMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	// A later sync without a size keeps the stored one.
	msgs[0].SizeBytes = 0
	if err := db.UpsertSyncMessages(ctx, msgs[:1]); err != nil {
		t.Fatal(err)
	}

	subjects := func(filters models.EmailFilters) []string {
		t.Helper()
		page, err := db.GetEmailsRangeFilteredForUser(ctx, "default", "acc_inbox", 0, 10, filters)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range page.Emails {
			out = append(out, e.Subject)
		}
		return out
	}
	if got := subjects(models.EmailFilters{MinSizeBytes: 1 << 20}); len(got) != 1 || got[0] != "Site photos" {
		t.Fatalf("larger than 1 MB = %v", got)
	}
	// A message with no known size is not reported as small.
	if got := subjects(models.EmailFilters{MaxSizeBytes: 1 << 20}); len(got) != 1 || got[0] != "Lunch" {
		t.Fatalf("smaller than 1 MB = %v", got)
	}

	uids, err := db.ListFolderUIDsMissingSize(ctx, "acc_inbox", 10)
	if err != nil || len(uids) != 1 || uids[0] != 3 {
		t.Fatalf("UIDs missing size = %v, %v", uids, err)
	}
	if err := db.SetFolderMessageSizes(ctx, "acc_inbox", map[uint32]int64{3: 512}); err != nil {
		t.Fatal(err)
	}
	if got := subjects(models.EmailFilters{MaxSizeBytes: 1 << 20}); len(got) != 2 {
		t.Fatalf("smaller than 1 MB after lookup = %v", got)
	}
	if uids, _ := db.ListFolderUIDsMissingSize(ctx, "acc_inbox", 10); len(uids) != 0 {
		t.Fatalf("UIDs still missing size = %v", uids)
	}
}

func TestProviderMessageSizesAreStoredAndLookedUp(t *testing.T) {
	ctx := context.Background()
	db := newContactsTestDB(t)
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, user_id, provider, email_address) VALUES ('acc', 'default', 'gmail', 'ana@example.com')`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFolders(ctx, []UpsertFolderInput{{ID: "acc_inbox", AccountID: "acc", RemoteID: "INBOX", ProviderRemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := db.UpsertProviderSyncMessages(ctx, []ProviderSyncMessage{
		{AccountID: "acc", FolderID: "acc_inbox", ProviderMessageID: "p-1", InternetMessageID: "<one@example.com>", Subject: "Invoice", DateSent: now, SizeBytes: 4096},
		{AccountID: "acc", FolderID: "acc_inbox", ProviderMessageID: "p-2", InternetMessageID: "<two@example.com>", Subject: "Agenda", DateSent: now},
	}); err != nil {
		t.Fatal(err)
	}
	ids, err := db.ListProviderMessagesMissingSize(ctx, "acc", 10)
	if err != nil || len(ids) != 1 || ids[0] != "p-2" {
		t.Fatalf("missing size = %v, %v", ids, err)
	}
	if err := db.SetProviderMessageSizes(ctx, "acc", map[string]int64{"p-2": MessageSizeUnavailable}); err != nil {
		t.Fatal(err)
	}
	if ids, _ := db.ListProviderMessagesMissingSize(ctx, "acc", 10); len(ids) != 0 {
		t.Fatalf("a size the provider can't report is asked for again: %v", ids)
	}
	// An update without a size keeps the stored one.
	if _, err := db.UpsertProviderSyncMessages(ctx, []ProviderSyncMessage{{AccountID: "acc", FolderID: "acc_inbox", ProviderMessageID: "p-1", InternetMessageID: "<one@example.com>", Subject: "Invoice", DateSent: now}}); err != nil {
		t.Fatal(err)
	}
	var size int64
	if err := db.Read().QueryRowContext(ctx, `SELECT size_bytes FROM messages WHERE remote_message_id = 'p-1'`).Scan(&size); err != nil || size != 4096 {
		t.Fatalf("size = %d, %v", size, err)
	}
}
