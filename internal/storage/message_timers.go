package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Message timers are local-only: Gmail, Graph and IMAP have no snooze or
// follow-up state, so other mail apps keep showing these threads.
const (
	MessageTimerSnooze   = "snooze"
	MessageTimerFollowUp = "followup"
)

const messageTimersSchema = `CREATE TABLE IF NOT EXISTS message_timers (
    id INTEGER PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    thread_key TEXT NOT NULL,
    message_id INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('snooze', 'followup')),
    due_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    UNIQUE (account_id, thread_key, kind)
)`

const messageTimersDueIndex = `CREATE INDEX IF NOT EXISTS idx_message_timers_due ON message_timers(due_at)`

// migrateV122ToV123 adds message_timers. It only creates a table, so rolling
// back is DROP TABLE message_timers.
func migrateV122ToV123(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{messageTimersSchema, messageTimersDueIndex} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("create message_timers: %w", err)
		}
	}
	if err := markSchemaVersion(tx, 123); err != nil {
		return err
	}
	return tx.Commit()
}

// timerSQLTime stores timer times as UTC "YYYY-MM-DD HH:MM:SS" text, which
// sorts as text and parses with SQLite's datetime(). messages.date_received
// holds mixed formats, so comparisons with it go through datetime().
func timerSQLTime(t time.Time) string {
	return t.UTC().Format(time.DateTime)
}

type MessageTimer struct {
	ID        int64
	AccountID string
	ThreadKey string
	MessageID int64
	Kind      string
	DueAt     time.Time
	CreatedAt time.Time
}

// SetMessageTimer snoozes or sets a follow-up on the thread of messageID,
// replacing any timer of the same kind on that thread.
func (db *DB) SetMessageTimer(ctx context.Context, userID string, messageID int64, kind string, dueAt, now time.Time) (MessageTimer, error) {
	if kind != MessageTimerSnooze && kind != MessageTimerFollowUp {
		return MessageTimer{}, fmt.Errorf("unknown timer kind %q", kind)
	}
	var timer MessageTimer
	err := db.Read().QueryRowContext(ctx, `
		SELECT m.account_id, COALESCE(NULLIF(m.thread_id, ''), printf('msg:%d', m.id))
		FROM messages m JOIN accounts a ON a.id = m.account_id
		WHERE m.id = ? AND a.user_id = ?`, messageID, userID).Scan(&timer.AccountID, &timer.ThreadKey)
	if err != nil {
		return MessageTimer{}, err
	}
	timer.MessageID, timer.Kind, timer.DueAt, timer.CreatedAt = messageID, kind, dueAt.UTC(), now.UTC()
	if err := db.Write().QueryRowContext(ctx, `
		INSERT INTO message_timers (account_id, thread_key, message_id, kind, due_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (account_id, thread_key, kind) DO UPDATE SET
			message_id = excluded.message_id, due_at = excluded.due_at, created_at = excluded.created_at
		RETURNING id`, timer.AccountID, timer.ThreadKey, messageID, kind, timerSQLTime(timer.DueAt), timerSQLTime(timer.CreatedAt)).Scan(&timer.ID); err != nil {
		return MessageTimer{}, err
	}
	if kind == MessageTimerSnooze {
		return timer, db.refreshInboxThreadState(ctx, timer.AccountID, []string{timer.ThreadKey})
	}
	return timer, nil
}

// ClearMessageTimer removes the timer of kind on messageID's thread. It reports
// whether one existed.
func (db *DB) ClearMessageTimer(ctx context.Context, userID string, messageID int64, kind string) (bool, error) {
	var accountID, threadKey string
	err := db.Read().QueryRowContext(ctx, `
		SELECT m.account_id, COALESCE(NULLIF(m.thread_id, ''), printf('msg:%d', m.id))
		FROM messages m JOIN accounts a ON a.id = m.account_id
		WHERE m.id = ? AND a.user_id = ?`, messageID, userID).Scan(&accountID, &threadKey)
	if err != nil {
		return false, err
	}
	return db.deleteMessageTimer(ctx, accountID, threadKey, kind)
}

func (db *DB) deleteMessageTimer(ctx context.Context, accountID, threadKey, kind string) (bool, error) {
	result, err := db.Write().ExecContext(ctx, `DELETE FROM message_timers WHERE account_id = ? AND thread_key = ? AND kind = ?`, accountID, threadKey, kind)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	if n > 0 && kind == MessageTimerSnooze {
		return true, db.refreshInboxThreadState(ctx, accountID, []string{threadKey})
	}
	return n > 0, nil
}

// DueMessageTimers lists timers due at or before now across every account in this database.
func (db *DB) DueMessageTimers(ctx context.Context, now time.Time, limit int) ([]MessageTimer, error) {
	rows, err := db.Read().QueryContext(ctx, `
		SELECT id, account_id, thread_key, message_id, kind, due_at, created_at
		FROM message_timers WHERE due_at <= ? ORDER BY due_at LIMIT ?`, timerSQLTime(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var timers []MessageTimer
	for rows.Next() {
		var t MessageTimer
		if err := rows.Scan(&t.ID, &t.AccountID, &t.ThreadKey, &t.MessageID, &t.Kind, &t.DueAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		timers = append(timers, t)
	}
	return timers, rows.Err()
}

// FinishMessageTimer deletes a due timer by id, so a timer replaced after it
// was listed is left alone.
func (db *DB) FinishMessageTimer(ctx context.Context, timer MessageTimer) (bool, error) {
	result, err := db.Write().ExecContext(ctx, `DELETE FROM message_timers WHERE id = ? AND due_at = ?`, timer.ID, timerSQLTime(timer.DueAt))
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	if n > 0 && timer.Kind == MessageTimerSnooze {
		return true, db.refreshInboxThreadState(ctx, timer.AccountID, []string{timer.ThreadKey})
	}
	return n > 0, nil
}

// ThreadRepliedSince reports whether the thread has a message dated after
// since from an address other than the account's own.
func (db *DB) ThreadRepliedSince(ctx context.Context, accountID, threadKey string, since time.Time) (bool, error) {
	var replied int
	err := db.Read().QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM messages m JOIN accounts a ON a.id = m.account_id
			WHERE m.account_id = ? AND COALESCE(NULLIF(m.thread_id, ''), printf('msg:%d', m.id)) = ?
			  AND datetime(m.date_received) > datetime(?) AND lower(m.from_email) <> lower(a.email_address)
			  AND lower(m.from_email) NOT IN (SELECT lower(email) FROM account_identities WHERE account_id = a.id))`,
		accountID, threadKey, timerSQLTime(since)).Scan(&replied)
	return replied == 1, err
}

// RepliedSnoozes lists snoozes whose thread got a message from someone else
// after the snooze was set. The timer worker ends them early, so a reply is
// not hidden until the snooze runs out, whichever provider synced it.
func (db *DB) RepliedSnoozes(ctx context.Context, limit int) ([]MessageTimer, error) {
	rows, err := db.Read().QueryContext(ctx, `
		SELECT t.id, t.account_id, t.thread_key, t.message_id, t.kind, t.due_at, t.created_at
		FROM message_timers t JOIN accounts a ON a.id = t.account_id
		WHERE t.kind = 'snooze' AND EXISTS (
			SELECT 1 FROM messages m
			WHERE m.account_id = t.account_id AND COALESCE(NULLIF(m.thread_id, ''), printf('msg:%d', m.id)) = t.thread_key
			  AND datetime(m.date_received) > datetime(t.created_at) AND lower(m.from_email) <> lower(a.email_address)
			  AND lower(m.from_email) NOT IN (SELECT lower(email) FROM account_identities WHERE account_id = a.id))
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var timers []MessageTimer
	for rows.Next() {
		var t MessageTimer
		if err := rows.Scan(&t.ID, &t.AccountID, &t.ThreadKey, &t.MessageID, &t.Kind, &t.DueAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		timers = append(timers, t)
	}
	return timers, rows.Err()
}

// refreshInboxThreadState rebuilds the given threads in the account's inbox
// folders, where snoozed threads are left out of folder_thread_state.
func (db *DB) refreshInboxThreadState(ctx context.Context, accountID string, threadKeys []string) error {
	rows, err := db.Read().QueryContext(ctx, `SELECT id FROM folders WHERE account_id = ? AND role = 'inbox'`, accountID)
	if err != nil {
		return err
	}
	var folders []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		folders = append(folders, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, folderID := range folders {
		if err := db.refreshFolderThreadKeys(ctx, folderID, threadKeys); err != nil {
			return fmt.Errorf("refresh inbox %s: %w", strings.TrimSpace(folderID), err)
		}
	}
	return nil
}
