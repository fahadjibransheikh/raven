package storage

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

// The UNION ALL fast path must return exactly what the original sort-everything
// query returns, for every offset/limit, including equal timestamps across folders.
func TestUnifiedInboxUnionMatchesSortAllQuery(t *testing.T) {
	ctx := context.Background()
	db := newContactsTestDB(t)
	var folderIDs []string
	msgID := 0
	for a := 0; a < 3; a++ {
		acc := fmt.Sprintf("acc%d", a)
		if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, user_id, provider, email_address) VALUES (?, 'default', 'imap', ?)`, acc, acc+"@example.com"); err != nil {
			t.Fatal(err)
		}
		fid := acc + "_inbox"
		if err := db.UpsertFolders(ctx, []UpsertFolderInput{{ID: fid, AccountID: acc, RemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true}}); err != nil {
			t.Fatal(err)
		}
		folderIDs = append(folderIDs, fid)
		for i := 0; i < 25; i++ {
			msgID++
			// i/2 makes pairs share a timestamp, and accounts share timestamps too.
			ts := fmt.Sprintf("2026-03-01 10:%02d:00", i/2)
			if _, err := db.Write().ExecContext(ctx, `INSERT INTO messages (id, account_id, internet_message_id, subject, from_email, date_received) VALUES (?, ?, ?, ?, 'x@example.com', ?)`,
				msgID, acc, fmt.Sprintf("<m%d@example.com>", msgID), fmt.Sprintf("s%d", msgID), ts); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Write().ExecContext(ctx, `INSERT INTO message_folder_state (message_id, folder_id, remote_uid) VALUES (?, ?, ?)`, msgID, fid, msgID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Write().ExecContext(ctx, `INSERT INTO folder_thread_state (folder_id, thread_key, head_message_id, account_id, last_message_at, thread_count) VALUES (?, ?, ?, ?, ?, ?)`,
				fid, fmt.Sprintf("thread:%d", msgID), msgID, acc, ts, 1+i%3); err != nil {
				t.Fatal(err)
			}
		}
	}

	for _, c := range []struct{ offset, limit int }{{0, 50}, {0, 10}, {10, 10}, {37, 20}, {70, 50}, {74, 5}, {200, 10}} {
		old, err := db.listEmailsFromFolderThreadState(ctx, `JOIN folders f ON fts.folder_id = f.id
			JOIN accounts owner ON f.account_id = owner.id
			WHERE owner.user_id = ? AND f.role = ?`, []any{"default", "inbox"}, c.offset, c.limit)
		if err != nil {
			t.Fatal(err)
		}
		got, err := db.listEmailsFromFolderThreadStateUnion(ctx, folderIDs, c.offset, c.limit)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(old, got) {
			t.Fatalf("offset=%d limit=%d: union result differs from sort-all result\nold=%v\nnew=%v", c.offset, c.limit, ids(old), ids(got))
		}
	}

	// And the public entry point takes the fast path with the same answer.
	viaPublic, err := db.listEmailsUnfilteredForUser(ctx, "default", "inbox", 5, 20)
	if err != nil {
		t.Fatal(err)
	}
	direct, _ := db.listEmailsFromFolderThreadStateUnion(ctx, folderIDs, 5, 20)
	if len(viaPublic) != 20 || !reflect.DeepEqual(viaPublic, direct) {
		t.Fatalf("listEmailsUnfilteredForUser diverged: %v vs %v", ids(viaPublic), ids(direct))
	}
}

func ids[T any](emails []T) []any {
	out := make([]any, len(emails))
	for i, e := range emails {
		out[i] = reflect.ValueOf(e).FieldByName("ID").Interface()
	}
	return out
}
