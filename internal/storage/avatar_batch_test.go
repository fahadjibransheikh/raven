package storage

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

// scanEmailRows hydrates sender avatars with the batch helper; it must give the
// same result as the per-contact helper it replaced for every avatar state.
func TestBatchAvatarHydrationMatchesPerContact(t *testing.T) {
	ctx := context.Background()
	db := newContactsTestDB(t)
	future, past := time.Now().Add(time.Hour), time.Now().Add(-time.Hour)
	senders := map[string]func(hash, email string) error{
		"found@example.com": func(h, e string) error {
			return db.SaveSenderAvatarFound(ctx, h, e, "gravatar", "image/png", "", []byte{1, 2}, future, "found", "")
		},
		"expired@example.com": func(h, e string) error {
			return db.SaveSenderAvatarFound(ctx, h, e, "bimi", "image/png", "", []byte{1}, past, "found", "")
		},
		"missing@example.com": func(h, e string) error { return db.SaveSenderAvatarMissing(ctx, h, e, "gravatar", future, "missing", "") },
		"error@example.com":   func(h, e string) error { return db.SaveSenderAvatarError(ctx, h, e, "x", "boom", future, "error", "") },
	}
	var contacts []models.Contact
	for email, save := range senders {
		c := contactFromSender("", email)
		if err := save(c.AvatarHash, email); err != nil {
			t.Fatal(err)
		}
		contacts = append(contacts, c)
	}
	contacts = append(contacts, contactFromSender("Nobody", "unseen@example.com"), contactFromSender("", "found@example.com"))

	single := append([]models.Contact(nil), contacts...)
	for i := range single {
		db.hydrateContactAvatar(ctx, &single[i])
	}
	batch := append([]models.Contact(nil), contacts...)
	db.hydrateContactAvatars(ctx, batch)
	if !reflect.DeepEqual(single, batch) {
		t.Fatalf("batch hydration differs from per-contact:\nsingle=%+v\nbatch =%+v", single, batch)
	}
	if single[0].AvatarStatus == "" {
		t.Fatal("test did not exercise any avatar state")
	}
}

// Thread view and recipients use the batch path; sender, To and CC must all
// come back with their avatar state.
func TestThreadMessagesHydrateAvatarsInBatch(t *testing.T) {
	ctx := context.Background()
	db := newContactsTestDB(t)
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, user_id, provider, email_address) VALUES ('acc', 'default', 'imap', 'u@example.com')`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFolders(ctx, []UpsertFolderInput{{ID: "inbox", AccountID: "acc", RemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.UpsertSyncMessages(ctx, []SyncMessage{
		{AccountID: "acc", FolderID: "inbox", RemoteUID: 1, MessageID: "<t1@x>", Subject: "hi", FromEmail: "found@example.com", DateSent: now.Add(-time.Hour),
			ToRecipients: []Recipient{{Email: "to@example.com"}}, CCRecipients: []Recipient{{Email: "cc@example.com"}}},
		{AccountID: "acc", FolderID: "inbox", RemoteUID: 2, MessageID: "<t2@x>", InReplyTo: "<t1@x>", References: "<t1@x>", Subject: "hi", FromEmail: "unseen@example.com", DateSent: now,
			ToRecipients: []Recipient{{Email: "found@example.com"}}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{"found@example.com", "cc@example.com"} {
		if err := db.SaveSenderAvatarFound(ctx, contactFromSender("", e).AvatarHash, e, "gravatar", "image/png", "", []byte{1}, now.Add(time.Hour), "found", ""); err != nil {
			t.Fatal(err)
		}
	}
	var threadID string
	if err := db.Read().QueryRow(`SELECT thread_id FROM messages WHERE internet_message_id = '<t2@x>'`).Scan(&threadID); err != nil || threadID == "" {
		t.Fatalf("thread id %q: %v", threadID, err)
	}
	items, err := db.GetThreadMessages(ctx, "acc", threadID)
	if err != nil || len(items) != 2 {
		t.Fatalf("GetThreadMessages() = %d items, %v", len(items), err)
	}
	byFrom := map[string]models.ThreadItem{}
	for _, it := range items {
		byFrom[it.From.Email] = it
	}
	first, second := byFrom["found@example.com"], byFrom["unseen@example.com"]
	if first.From.AvatarURL == "" || first.From.AvatarStatus != "found" {
		t.Errorf("sender avatar not hydrated: %+v", first.From)
	}
	if second.From.AvatarURL != "" || second.From.AvatarStatus != "unknown" {
		t.Errorf("unseen sender should be unknown: %+v", second.From)
	}
	if len(first.CC) != 1 || first.CC[0].AvatarURL == "" {
		t.Errorf("cc avatar not hydrated: %+v", first.CC)
	}
	if len(second.To) != 1 || second.To[0].AvatarURL == "" {
		t.Errorf("to avatar not hydrated: %+v", second.To)
	}
	if len(first.To) != 1 || first.To[0].AvatarStatus != "unknown" {
		t.Errorf("unseen recipient should be unknown: %+v", first.To)
	}
}
