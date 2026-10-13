package storage

import "testing"

// A draft in a thread is marked, so the thread view can offer to keep
// editing it.
func TestThreadMessagesMarkDrafts(t *testing.T) {
	ctx := t.Context()
	db := newContactsTestDB(t)
	if _, err := db.Write().ExecContext(ctx, `
		INSERT INTO accounts (id, user_id, provider, email_address)
		VALUES ('acct', 'default', 'imap', 'ana@example.test')`); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := db.Write().ExecContext(ctx, `
		INSERT INTO folders (id, account_id, remote_id, name, role)
		VALUES ('inbox', 'acct', 'INBOX', 'Inbox', 'inbox'),
		       ('drafts', 'acct', 'Drafts', 'Drafts', 'drafts')`); err != nil {
		t.Fatalf("insert folders: %v", err)
	}
	if _, err := db.Write().ExecContext(ctx, `
		INSERT INTO messages (id, account_id, internet_message_id, thread_id, subject, from_email, date_sent)
		VALUES (601, 'acct', '<question@example.test>', 'picnic', 'Picnic', 'bo@example.test', '2026-01-01 10:00:00'),
		       (602, 'acct', '<answer@example.test>', 'picnic', 'Re: Picnic', 'ana@example.test', '2026-01-01 11:00:00')`); err != nil {
		t.Fatalf("insert messages: %v", err)
	}
	if _, err := db.Write().ExecContext(ctx, `
		INSERT INTO message_folder_state (message_id, folder_id, remote_uid, is_draft)
		VALUES (601, 'inbox', 1, 0), (602, 'drafts', 1, 1)`); err != nil {
		t.Fatalf("insert folder state: %v", err)
	}

	thread, err := db.GetThreadMessagesForUser(ctx, "acct", "picnic", "default")
	if err != nil || len(thread) != 2 {
		t.Fatalf("GetThreadMessagesForUser = %#v, %v", thread, err)
	}
	drafts := map[string]bool{}
	for _, item := range thread {
		drafts[item.ID] = item.IsDraft
	}
	if drafts["601"] || !drafts["602"] {
		t.Fatalf("drafts = %v, want only 602", drafts)
	}
}
