package storage

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// Mutations refresh folder_thread_state incrementally. Every scenario here
// applies one mutation to a seeded mailbox and asserts the stored thread
// state (and folders.unread_count) equals a from-scratch rebuild.

type threadStateRow struct {
	FolderID, ThreadKey            string
	Head                           int64
	Account, LastAt                string
	Count, IsRead, Starred, HasAtt int
}

func snapshotThreadState(t *testing.T, db *DB) []threadStateRow {
	t.Helper()
	rows, err := db.Read().Query(`SELECT folder_id, thread_key, head_message_id, account_id, COALESCE(last_message_at,''),
		thread_count, thread_is_read, thread_is_starred, thread_has_attachments
		FROM folder_thread_state ORDER BY folder_id, thread_key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []threadStateRow
	for rows.Next() {
		var r threadStateRow
		if err := rows.Scan(&r.FolderID, &r.ThreadKey, &r.Head, &r.Account, &r.LastAt, &r.Count, &r.IsRead, &r.Starred, &r.HasAtt); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func assertThreadStateMatchesFullRebuild(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	got := snapshotThreadState(t, db)
	var storedUnread = map[string]int{}
	frows, err := db.Read().Query(`SELECT id, unread_count FROM folders`)
	if err != nil {
		t.Fatal(err)
	}
	for frows.Next() {
		var id string
		var n int
		if err := frows.Scan(&id, &n); err != nil {
			t.Fatal(err)
		}
		storedUnread[id] = n
	}
	frows.Close()
	for id, n := range storedUnread {
		var actual int
		if err := db.Read().QueryRow(`SELECT COUNT(*) FROM message_folder_state WHERE folder_id = ? AND is_deleted = 0 AND is_read = 0`, id).Scan(&actual); err != nil {
			t.Fatal(err)
		}
		if actual != n {
			t.Errorf("folders.unread_count[%s] = %d, actual unread = %d", id, n, actual)
		}
	}
	for id := range storedUnread {
		if err := db.RefreshFolderThreadState(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	want := snapshotThreadState(t, db)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("incremental thread state differs from full rebuild\n got: %+v\nwant: %+v", got, want)
	}
}

// seedThreadStateDB: imap account, inbox/archive/trash/spam, 3 threads in the
// inbox (a: 3 msgs, b: 2 msgs, c: 1 msg) plus a singleton and one message that
// also lives in archive.
func seedThreadStateDB(t *testing.T) (*DB, map[string]int64) {
	t.Helper()
	ctx := context.Background()
	db := newContactsTestDB(t)
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, user_id, provider, email_address) VALUES ('acc', 'default', 'imap', 'u@example.com')`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFolders(ctx, []UpsertFolderInput{
		{ID: "inbox", AccountID: "acc", RemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true},
		{ID: "archive", AccountID: "acc", RemoteID: "Archive", Name: "Archive", Role: "archive", Selectable: true},
		{ID: "trash", AccountID: "acc", RemoteID: "Trash", Name: "Trash", Role: "trash", Selectable: true},
		{ID: "spam", AccountID: "acc", RemoteID: "Spam", Name: "Spam", Role: "spam", Selectable: true},
	}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	names := []string{"a1", "a2", "a3", "b1", "b2", "c1", "s1"}
	thread := map[string]string{"a1": "ta", "a2": "ta", "a3": "ta", "b1": "tb", "b2": "tb", "c1": "tc", "s1": ""}
	var msgs []SyncMessage
	for i, n := range names {
		msgs = append(msgs, SyncMessage{AccountID: "acc", FolderID: "inbox", RemoteUID: uint32(i + 1), MessageID: "<" + n + "@x>",
			Subject: "subject " + n, FromEmail: "f@x.com", DateSent: base.Add(time.Duration(i) * time.Hour), IsRead: i%2 == 0})
	}
	if err := db.UpsertSyncMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	// a2 also lives in archive.
	if err := db.UpsertSyncMessages(ctx, []SyncMessage{{AccountID: "acc", FolderID: "archive", RemoteUID: 1, MessageID: "<a2@x>",
		Subject: "subject a2", FromEmail: "f@x.com", DateSent: base.Add(time.Hour), IsRead: true}}); err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, n := range names {
		id, err := db.GetMessageLocalIDByInternetIDInternal(ctx, "acc", "<"+n+"@x>")
		if err != nil || id == 0 {
			t.Fatalf("id for %s: %d %v", n, id, err)
		}
		ids[n] = id
		if tk := thread[n]; tk != "" {
			if _, err := db.Write().ExecContext(ctx, `UPDATE messages SET thread_id = ? WHERE id = ?`, tk, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, f := range []string{"inbox", "archive", "trash", "spam"} {
		if _, err := db.RefreshFolderUnreadCount(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	assertThreadStateMatchesFullRebuild(t, db)
	return db, ids
}

func TestIncrementalThreadStateMatchesFullRebuild(t *testing.T) {
	ctx := context.Background()
	scenarios := []struct {
		name string
		run  func(t *testing.T, db *DB, id map[string]int64)
	}{
		{"read one of a thread", func(t *testing.T, db *DB, id map[string]int64) {
			must(t, db.SetMessagesReadAndQueue(ctx, []int64{id["a1"]}, true))
		}},
		{"unread (multi-folder message)", func(t *testing.T, db *DB, id map[string]int64) {
			must(t, db.SetMessagesReadAndQueue(ctx, []int64{id["a2"]}, false))
		}},
		{"read batch across threads", func(t *testing.T, db *DB, id map[string]int64) {
			must(t, db.SetMessagesReadAndQueue(ctx, []int64{id["a1"], id["b1"], id["c1"], id["s1"]}, true))
		}},
		{"star", func(t *testing.T, db *DB, id map[string]int64) {
			must(t, db.SetMessagesStarredAndQueue(ctx, []int64{id["b2"], id["s1"]}, true))
		}},
		{"unstar after star", func(t *testing.T, db *DB, id map[string]int64) {
			must(t, db.SetMessagesStarredAndQueue(ctx, []int64{id["b2"]}, true))
			must(t, db.SetMessagesStarredAndQueue(ctx, []int64{id["b2"]}, false))
		}},
		{"move to custom archive (partial thread)", func(t *testing.T, db *DB, id map[string]int64) {
			must(t, db.MoveMessagesAndQueue(ctx, []int64{id["a1"]}, "inbox", "archive"))
		}},
		{"archive whole thread", func(t *testing.T, db *DB, id map[string]int64) {
			must(t, db.MoveMessagesAndQueue(ctx, []int64{id["b1"], id["b2"]}, "inbox", "archive"))
		}},
		{"delete to trash", func(t *testing.T, db *DB, id map[string]int64) {
			must(t, db.MoveMessagesAndQueue(ctx, []int64{id["a3"], id["c1"]}, "inbox", "trash"))
		}},
		{"spam", func(t *testing.T, db *DB, id map[string]int64) {
			must(t, db.MoveMessagesAndQueue(ctx, []int64{id["a2"], id["s1"]}, "inbox", "spam"))
		}},
		{"move there and back", func(t *testing.T, db *DB, id map[string]int64) {
			must(t, db.MoveMessagesAndQueue(ctx, []int64{id["a1"]}, "inbox", "trash"))
			must(t, db.MoveMessagesAndQueue(ctx, []int64{id["a1"]}, "trash", "inbox"))
		}},
		{"permanent delete", func(t *testing.T, db *DB, id map[string]int64) {
			must(t, db.PermanentlyDeleteMessagesAndQueue(ctx, []int64{id["a1"], id["s1"]}, "inbox"))
		}},
		{"label add (no thread effect)", func(t *testing.T, db *DB, id map[string]int64) {
			_, err := db.AddMessageLabel(ctx, id["a1"], "acc", LabelInput{Name: "work"})
			must(t, err)
		}},
		{"sync upsert: reply joins thread, flags change", func(t *testing.T, db *DB, id map[string]int64) {
			must(t, db.UpsertSyncMessages(ctx, []SyncMessage{
				{AccountID: "acc", FolderID: "inbox", RemoteUID: 20, MessageID: "<new@x>", InReplyTo: "<b2@x>", References: "<b1@x> <b2@x>",
					Subject: "subject b2", FromEmail: "f@x.com", DateSent: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)},
				{AccountID: "acc", FolderID: "inbox", RemoteUID: 3, MessageID: "<a3@x>", Subject: "subject a3", FromEmail: "f@x.com",
					DateSent: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC), IsRead: false, IsStarred: true},
			}))
			_, err := db.RefreshFolderUnreadCount(ctx, "inbox")
			must(t, err)
		}},
		{"sync upsert: parent arrives after child (thread merge)", func(t *testing.T, db *DB, id map[string]int64) {
			must(t, db.UpsertSyncMessages(ctx, []SyncMessage{{AccountID: "acc", FolderID: "inbox", RemoteUID: 30, MessageID: "<child@x>",
				InReplyTo: "<parent@x>", References: "<parent@x>", Subject: "late", FromEmail: "f@x.com", DateSent: time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)}}))
			must(t, db.UpsertSyncMessages(ctx, []SyncMessage{{AccountID: "acc", FolderID: "inbox", RemoteUID: 31, MessageID: "<parent@x>",
				Subject: "late", FromEmail: "f@x.com", DateSent: time.Date(2026, 3, 2, 23, 0, 0, 0, time.UTC)}}))
			_, err := db.RefreshFolderUnreadCount(ctx, "inbox")
			must(t, err)
		}},
		{"imap flag changes", func(t *testing.T, db *DB, id map[string]int64) {
			n, err := db.BatchUpdateFlags(ctx, "inbox", []FlagUpdate{{UID: 1, IsRead: true, IsStarred: true}, {UID: 2, IsRead: false}, {UID: 4, IsRead: true}})
			must(t, err)
			if n == 0 {
				t.Fatal("expected flag changes")
			}
		}},
		{"expunge non-head member", func(t *testing.T, db *DB, id map[string]int64) {
			_, err := db.RemoveExpungedUIDs(ctx, "inbox", []uint32{1})
			must(t, err)
		}},
		{"expunge head and singleton and whole thread", func(t *testing.T, db *DB, id map[string]int64) {
			_, err := db.RemoveExpungedUIDs(ctx, "inbox", []uint32{3, 4, 5, 7})
			must(t, err)
		}},
		{"expunge multi-folder message", func(t *testing.T, db *DB, id map[string]int64) {
			_, err := db.RemoveExpungedUIDs(ctx, "archive", []uint32{1})
			must(t, err)
		}},
		{"mark folder read", func(t *testing.T, db *DB, id map[string]int64) {
			_, err := db.MarkFolderReadAndQueueForUser(ctx, "default", "inbox", time.Now().Add(time.Hour))
			must(t, err)
		}},
		{"delete draft", func(t *testing.T, db *DB, id map[string]int64) {
			must(t, db.UpsertSyncMessages(ctx, []SyncMessage{{AccountID: "acc", FolderID: "inbox", RemoteUID: 40, MessageID: "<d@x>", Subject: "subject b1",
				FromEmail: "f@x.com", DateSent: time.Now(), IsDraft: true, IsRead: true}}))
			_, err := db.RefreshFolderUnreadCount(ctx, "inbox")
			must(t, err)
			_, err = db.DeleteDraftMessage(ctx, "acc", "<d@x>")
			must(t, err)
		}},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			db, ids := seedThreadStateDB(t)
			sc.run(t, db, ids)
			assertThreadStateMatchesFullRebuild(t, db)
		})
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(fmt.Sprint(err))
	}
}

// The sidebar counts come from an unread-only index, and folders.unread_count
// is maintained by the mutation paths; both must track real unread mail.
func TestSidebarUnreadCountsTrackMutations(t *testing.T) {
	ctx := context.Background()
	db, id := seedThreadStateDB(t)
	brute := func(role string) int {
		var n int
		if err := db.Read().QueryRow(`SELECT COUNT(*) FROM message_folder_state mfs JOIN folders f ON f.id = mfs.folder_id
			WHERE f.role = ? AND mfs.is_deleted = 0 AND mfs.is_read = 0`, role).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	check := func(step string) {
		t.Helper()
		counts, err := db.GetAllFolderUnreadCounts(ctx, "default")
		if err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"inbox", "archive", "trash", "spam"} {
			// Seed folder ids equal their roles, so the per-folder and unified
			// entries of the result land on the same key and add up.
			var stored int
			if err := db.Read().QueryRow(`SELECT unread_count FROM folders WHERE id = ?`, role).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if stored != brute(role) {
				t.Errorf("%s: stored %s = %d, actual %d", step, role, stored, brute(role))
			}
			if unified := counts[role] - stored; unified != brute(role) {
				t.Errorf("%s: unified %s = %d, actual %d", step, role, unified, brute(role))
			}
		}
	}
	check("seed")
	if brute("inbox") == 0 {
		t.Fatal("seed has no unread mail")
	}
	must(t, db.SetMessagesReadAndQueue(ctx, []int64{id["a2"]}, false))
	check("unread")
	must(t, db.SetMessagesReadAndQueue(ctx, []int64{id["b1"], id["c1"]}, true))
	check("read")
	must(t, db.MoveMessagesAndQueue(ctx, []int64{id["a2"], id["b2"]}, "inbox", "trash"))
	check("move")
	must(t, db.PermanentlyDeleteMessagesAndQueue(ctx, []int64{id["a2"]}, "trash"))
	check("permanent delete")
	_, err := db.RemoveExpungedUIDs(ctx, "inbox", []uint32{1, 2})
	must(t, err)
	check("expunge")

	// An Outlook folder that is only partly downloaded reports the provider's count.
	if _, err := db.Write().Exec(`UPDATE accounts SET provider = 'outlook'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().Exec(`UPDATE folders SET provider_remote_id = 'remote-inbox', total_count = 1000, unread_count = 77 WHERE id = 'inbox'`); err != nil {
		t.Fatal(err)
	}
	counts, err := db.GetAllFolderUnreadCounts(ctx, "default")
	if err != nil || counts["inbox"]-77 != 77 { // stored 77 + unified 77, same key
		t.Fatalf("partial outlook inbox = %d, %v; want provider count 77 for both entries", counts["inbox"], err)
	}
}
