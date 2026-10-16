package storage

import (
	"context"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/models"
)

func TestSearchFiltersMatchPercentAndUnderscoreLiterally(t *testing.T) {
	ctx := context.Background()
	db := newContactsTestDB(t)
	if _, err := db.Write().ExecContext(ctx, `
		INSERT INTO accounts (id, user_id, provider, email_address)
		VALUES ('acc', 'default', 'outlook', 'owner@example.com')`); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if err := db.UpsertFolders(ctx, []UpsertFolderInput{{ID: "acc_inbox", AccountID: "acc", Name: "Inbox", Role: "inbox", Selectable: true}}); err != nil {
		t.Fatalf("UpsertFolders() error = %v", err)
	}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	var msgs []ProviderSyncMessage
	for i, m := range []struct{ subject, from string }{
		{"50% off today", "first_name@example.com"},
		{"500 items shipped", "firstxname@example.com"},
	} {
		msgs = append(msgs, ProviderSyncMessage{
			AccountID: "acc", FolderID: "acc_inbox", ProviderMessageID: m.from,
			InternetMessageID: "<" + m.from + ">", Subject: m.subject, FromEmail: m.from,
			DateSent: now.Add(time.Duration(i) * time.Minute), DateReceived: now.Add(time.Duration(i) * time.Minute),
			ToRecipients: []Recipient{{Email: "owner@example.com"}},
		})
	}
	if _, err := db.UpsertProviderSyncMessages(ctx, msgs); err != nil {
		t.Fatalf("UpsertProviderSyncMessages() error = %v", err)
	}

	for name, filters := range map[string]models.EmailFilters{
		"subject %": {Subject: "50%"},
		"from _":    {From: "first_name"},
	} {
		page, err := db.GetEmailsRangeFilteredForUser(ctx, "default", "acc_inbox", 0, 10, filters)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if page.TotalCount != 1 {
			t.Fatalf("%s: matched %d messages, want 1 (wildcard not escaped)", name, page.TotalCount)
		}
	}
}
