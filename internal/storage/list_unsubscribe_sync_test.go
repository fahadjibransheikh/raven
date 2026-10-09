package storage

import (
	"context"
	"testing"
	"time"
)

func TestSyncUpsertsKeepStoredListUnsubscribeWhenHeadersAbsent(t *testing.T) {
	ctx := context.Background()
	db := newContactsTestDB(t)
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, user_id, provider, email_address) VALUES ('acc', 'default', 'imap', 'u@example.com')`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFolders(ctx, []UpsertFolderInput{{ID: "inbox", AccountID: "acc", RemoteID: "INBOX", ProviderRemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true}}); err != nil {
		t.Fatal(err)
	}
	read := func(internetID string) (string, string) {
		t.Helper()
		var lu, post string
		if err := db.Read().QueryRowContext(ctx, `SELECT list_unsubscribe, list_unsubscribe_post FROM messages WHERE internet_message_id = ?`, internetID).Scan(&lu, &post); err != nil {
			t.Fatal(err)
		}
		return lu, post
	}

	imap := SyncMessage{AccountID: "acc", FolderID: "inbox", RemoteUID: 1, MessageID: "<imap@x>", Subject: "s", FromEmail: "f@x.com", DateSent: time.Now(),
		ListUnsubscribe: "<https://x/u>", ListUnsubscribePost: "List-Unsubscribe=One-Click"}
	prov := ProviderSyncMessage{AccountID: "acc", FolderID: "inbox", ProviderMessageID: "p1", InternetMessageID: "<prov@x>", Subject: "s", FromEmail: "f@x.com",
		DateSent: time.Now(), DateReceived: time.Now(),
		ListUnsubscribe: "<mailto:u@x>", ListUnsubscribePost: ""}

	for round := 0; round < 2; round++ {
		if err := db.UpsertSyncMessages(ctx, []SyncMessage{imap}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.UpsertProviderSyncMessages(ctx, []ProviderSyncMessage{prov}); err != nil {
			t.Fatal(err)
		}
		if round == 0 {
			if lu, post := read("<imap@x>"); lu != "<https://x/u>" || post != "List-Unsubscribe=One-Click" {
				t.Fatalf("imap insert stored %q / %q", lu, post)
			}
			if lu, _ := read("<prov@x>"); lu != "<mailto:u@x>" {
				t.Fatalf("provider insert stored %q", lu)
			}
			// A later sync that did not fetch the headers must not wipe them.
			imap.ListUnsubscribe, imap.ListUnsubscribePost = "", ""
			prov.ListUnsubscribe, prov.ListUnsubscribePost = "", ""
		}
	}
	if lu, post := read("<imap@x>"); lu != "<https://x/u>" || post != "List-Unsubscribe=One-Click" {
		t.Errorf("imap upsert with empty headers wiped stored: %q / %q", lu, post)
	}
	if lu, _ := read("<prov@x>"); lu != "<mailto:u@x>" {
		t.Errorf("provider upsert with empty headers wiped stored: %q", lu)
	}

	// A changed value does replace.
	imap.ListUnsubscribe, imap.ListUnsubscribePost = "<https://x/v2>", ""
	if err := db.UpsertSyncMessages(ctx, []SyncMessage{imap}); err != nil {
		t.Fatal(err)
	}
	if lu, post := read("<imap@x>"); lu != "<https://x/v2>" || post != "" {
		t.Errorf("changed headers not stored: %q / %q", lu, post)
	}
}

func TestSyncUpsertsKeepStoredDeliveredToWhenAbsent(t *testing.T) {
	ctx := context.Background()
	db := newContactsTestDB(t)
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, user_id, provider, email_address) VALUES ('acc', 'default', 'imap', 'u@example.com')`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFolders(ctx, []UpsertFolderInput{{ID: "inbox", AccountID: "acc", RemoteID: "INBOX", ProviderRemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true}}); err != nil {
		t.Fatal(err)
	}
	read := func(internetID string) string {
		t.Helper()
		var d string
		if err := db.Read().QueryRowContext(ctx, `SELECT delivered_to FROM messages WHERE internet_message_id = ?`, internetID).Scan(&d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	imap := SyncMessage{AccountID: "acc", FolderID: "inbox", RemoteUID: 1, MessageID: "<imap@x>", Subject: "s", FromEmail: "f@x.com", DateSent: time.Now(), DeliveredTo: "a@x"}
	prov := ProviderSyncMessage{AccountID: "acc", FolderID: "inbox", ProviderMessageID: "p1", InternetMessageID: "<prov@x>", Subject: "s", FromEmail: "f@x.com",
		DateSent: time.Now(), DateReceived: time.Now(), DeliveredTo: "b@x"}
	for round := 0; round < 2; round++ {
		if err := db.UpsertSyncMessages(ctx, []SyncMessage{imap}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.UpsertProviderSyncMessages(ctx, []ProviderSyncMessage{prov}); err != nil {
			t.Fatal(err)
		}
		if round == 0 {
			if read("<imap@x>") != "a@x" || read("<prov@x>") != "b@x" {
				t.Fatalf("insert stored %q / %q", read("<imap@x>"), read("<prov@x>"))
			}
			imap.DeliveredTo, prov.DeliveredTo = "", ""
		}
	}
	if read("<imap@x>") != "a@x" || read("<prov@x>") != "b@x" {
		t.Errorf("empty upsert wiped stored: %q / %q", read("<imap@x>"), read("<prov@x>"))
	}
}
