package storage

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/models"
)

func listedIDs(t *testing.T, db *DB, folderID string, filters models.EmailFilters) []int64 {
	t.Helper()
	page, err := db.GetEmailsRangeFilteredForUserWithTotal(context.Background(), "default", folderID, 0, 50, filters, -1)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, e := range page.Emails {
		id, err := strconv.ParseInt(e.ID, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func indexOfEmail(page *models.EmailPage, id string) int {
	for i, e := range page.Emails {
		if e.ID == id {
			return i
		}
	}
	return -100
}

func containsID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func TestSnoozeHidesThreadFromInboxListsAndCountsOnly(t *testing.T) {
	ctx := context.Background()
	db := seedBulkInbox(t, 3)
	if _, err := db.Write().ExecContext(ctx, `
		UPDATE message_folder_state SET is_read = 0;
		UPDATE messages SET date_received = '2026-01-0' || id || ' 10:00:00';
		INSERT INTO folders (id, account_id, remote_id, name, role) VALUES ('archive', 'acc', 'Archive', 'Archive', 'archive');
		INSERT INTO message_folder_state (message_id, folder_id, remote_uid, is_read, synced_at) VALUES (2, 'archive', 2, 0, CURRENT_TIMESTAMP);`); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{"inbox", "archive"} {
		if err := db.RefreshFolderThreadState(ctx, folder); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	if _, err := db.SetMessageTimer(ctx, "default", 2, MessageTimerSnooze, now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}

	assertHidden := func(stage string) {
		t.Helper()
		for _, folder := range []string{"inbox"} {
			if ids := listedIDs(t, db, folder, models.EmailFilters{}); containsID(ids, 2) || len(ids) != 2 {
				t.Fatalf("%s: %s list = %v, want 1 and 3", stage, folder, ids)
			}
			if ids := listedIDs(t, db, folder, models.EmailFilters{Unread: true}); containsID(ids, 2) || len(ids) != 2 {
				t.Fatalf("%s: %s unread list = %v, want 1 and 3", stage, folder, ids)
			}
		}
		if n, err := db.GetFolderEmailCountUnfiltered(ctx, "inbox"); err != nil || n != 2 {
			t.Fatalf("%s: inbox count = %d, %v", stage, n, err)
		}
		if n, err := db.GetFolderEmailCountFilteredForUser(ctx, "default", "inbox", models.EmailFilters{}); err != nil || n != 2 {
			t.Fatalf("%s: unified inbox count = %d, %v", stage, n, err)
		}
		if n, err := db.GetFolderEmailCountFilteredForUser(ctx, "default", "inbox", models.EmailFilters{Unread: true}); err != nil || n != 2 {
			t.Fatalf("%s: unified unread inbox count = %d, %v", stage, n, err)
		}
		if ids := listedIDs(t, db, "archive", models.EmailFilters{}); !containsID(ids, 2) {
			t.Fatalf("%s: archive list = %v, want the snoozed message too", stage, ids)
		}
	}
	assertHidden("after snooze")
	// Message 1 is the oldest; with 2 snoozed only 3 is newer, so a one-row window around it starts at 1.
	for _, around := range []func() (*models.EmailPage, error){
		func() (*models.EmailPage, error) { return db.GetEmailsAroundEmail(ctx, "inbox", "1", 1) },
		func() (*models.EmailPage, error) {
			return db.GetEmailsAroundEmailForUser(ctx, "default", "inbox", "1", 1)
		},
	} {
		page, err := around()
		if err != nil || indexOfEmail(page, "1") != 0 {
			t.Fatalf("window around message 1 = %+v, %v", page, err)
		}
	}
	if ids := listedIDs(t, db, "snoozed", models.EmailFilters{}); len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("snoozed view = %v, want [2]", ids)
	}
	if ids := listedIDs(t, db, "snoozed", models.EmailFilters{Unread: true}); len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("filtered snoozed view = %v, want [2]", ids)
	}
	if n, err := db.GetFolderEmailCountFilteredForUser(ctx, "default", "snoozed", models.EmailFilters{}); err != nil || n != 1 {
		t.Fatalf("snoozed count = %d, %v", n, err)
	}
	if err := db.RefreshFolderThreadState(ctx, "inbox"); err != nil {
		t.Fatal(err)
	}
	assertHidden("after full rebuild")

	if due, err := db.DueMessageTimers(ctx, now, 10); err != nil || len(due) != 0 {
		t.Fatalf("due before time = %v, %v", due, err)
	}
	due, err := db.DueMessageTimers(ctx, now.Add(2*time.Hour), 10)
	if err != nil || len(due) != 1 || due[0].MessageID != 2 || due[0].Kind != MessageTimerSnooze {
		t.Fatalf("due = %#v, %v", due, err)
	}
	if done, err := db.FinishMessageTimer(ctx, due[0]); err != nil || !done {
		t.Fatalf("finish = %v, %v", done, err)
	}
	if ids := listedIDs(t, db, "inbox", models.EmailFilters{}); !containsID(ids, 2) {
		t.Fatalf("inbox after snooze ended = %v", ids)
	}
}

func TestFollowUpDoesNotHideAndReplyDetection(t *testing.T) {
	ctx := context.Background()
	db := seedBulkInbox(t, 2)
	if err := db.RefreshFolderThreadState(ctx, "inbox"); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	timer, err := db.SetMessageTimer(ctx, "default", 1, MessageTimerFollowUp, created.Add(24*time.Hour), created)
	if err != nil {
		t.Fatal(err)
	}
	if ids := listedIDs(t, db, "inbox", models.EmailFilters{}); !containsID(ids, 1) {
		t.Fatalf("follow-up hid the thread: %v", ids)
	}
	if replied, err := db.ThreadRepliedSince(ctx, "acc", timer.ThreadKey, created); err != nil || replied {
		t.Fatalf("replied before any reply = %v, %v", replied, err)
	}
	// The user's own message in the thread is not a reply.
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO messages (id, account_id, internet_message_id, thread_id, subject, from_email, date_received)
		VALUES (10, 'acc', '<own@x>', 't1', 's', 'U@example.com', '2026-01-01 13:00:00')`); err != nil {
		t.Fatal(err)
	}
	if replied, _ := db.ThreadRepliedSince(ctx, "acc", timer.ThreadKey, created); replied {
		t.Fatal("own message counted as a reply")
	}
	// Same day, earlier, written in RFC 3339 form: text comparison would call it newer.
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO messages (id, account_id, internet_message_id, thread_id, subject, from_email, date_received)
		VALUES (12, 'acc', '<early@x>', 't1', 's', 'them@example.org', '2026-01-01T11:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if replied, _ := db.ThreadRepliedSince(ctx, "acc", timer.ThreadKey, created); replied {
		t.Fatal("earlier RFC 3339 message counted as a reply")
	}
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO messages (id, account_id, internet_message_id, thread_id, subject, from_email, date_received)
		VALUES (11, 'acc', '<reply@x>', 't1', 's', 'them@example.org', '2026-01-01 14:00:00')`); err != nil {
		t.Fatal(err)
	}
	if replied, err := db.ThreadRepliedSince(ctx, "acc", timer.ThreadKey, created); err != nil || !replied {
		t.Fatalf("reply not detected = %v, %v", replied, err)
	}
}

func TestReplyInSnoozedThreadIsListedForEarlyWake(t *testing.T) {
	ctx := context.Background()
	db := seedBulkInbox(t, 2)
	if err := db.RefreshFolderThreadState(ctx, "inbox"); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if _, err := db.SetMessageTimer(ctx, "default", 1, MessageTimerSnooze, created.Add(72*time.Hour), created); err != nil {
		t.Fatal(err)
	}
	// Older mail found late, and the user's own message, do not end the snooze.
	if _, err := db.Write().ExecContext(ctx, `
		INSERT INTO messages (id, account_id, internet_message_id, thread_id, subject, from_email, date_received) VALUES
			(20, 'acc', '<old@x>', 't1', 's', 'them@example.org', '2026-01-01 09:00:00'),
			(21, 'acc', '<own@x>', 't1', 's', 'u@example.com', '2026-01-02 09:00:00')`); err != nil {
		t.Fatal(err)
	}
	if replied, err := db.RepliedSnoozes(ctx, 10); err != nil || len(replied) != 0 {
		t.Fatalf("replied = %#v, %v; want none", replied, err)
	}
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO messages (id, account_id, internet_message_id, thread_id, subject, from_email, date_received)
		VALUES (22, 'acc', '<new@x>', 't1', 's', 'them@example.org', '2026-01-02 10:00:00')`); err != nil {
		t.Fatal(err)
	}
	replied, err := db.RepliedSnoozes(ctx, 10)
	if err != nil || len(replied) != 1 || replied[0].ThreadKey != "t1" {
		t.Fatalf("replied = %#v, %v; want the t1 snooze", replied, err)
	}
}
