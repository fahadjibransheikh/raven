package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// calendar_reminders records which occurrence start times already raised a
// "meeting about to start" notification. It is keyed by (source, remote id,
// start) rather than the cached event row so that a re-sync, or a move to a
// new time, never loses or wrongly keeps the record. Providers hand back each
// occurrence of a recurring event as its own event, so this is per occurrence.
const calendarRemindersSchema = `CREATE TABLE IF NOT EXISTS calendar_reminders (
    source_id TEXT NOT NULL REFERENCES calendar_sources(id) ON DELETE CASCADE,
    remote_id TEXT NOT NULL,
    start_at DATETIME NOT NULL,
    sent_at DATETIME NOT NULL,
    PRIMARY KEY (source_id, remote_id, start_at)
)`

// migrateV124ToV125 adds calendar_reminders. It only creates a table, so
// rolling back is DROP TABLE calendar_reminders.
func migrateV124ToV125(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(calendarRemindersSchema); err != nil {
		return fmt.Errorf("create calendar_reminders: %w", err)
	}
	if err := markSchemaVersion(tx, 125); err != nil {
		return err
	}
	return tx.Commit()
}

// CalendarReminder is one upcoming timed occurrence that has not been announced yet.
type CalendarReminder struct {
	EventID   string
	UserID    string
	AccountID string
	SourceID  string
	RemoteID  string
	Summary   string
	Location  string
	StartAt   time.Time
	// Description and OnlineMeetingJSON are read so the reminder can carry a join link.
	Description       string
	OnlineMeetingJSON string
}

// UpcomingCalendarReminders returns timed occurrences that start after now and
// no later than now+horizon and have not been announced. It leaves out
// cancelled and declined events and hidden or deselected calendars. Events
// that already started never qualify, so a late start never announces them.
func (db *DB) UpcomingCalendarReminders(ctx context.Context, now time.Time, horizon time.Duration, limit int) ([]CalendarReminder, error) {
	rows, err := db.Read().QueryContext(ctx, `
		SELECT event.id, event.user_id, source.account_id, event.source_id, event.remote_id,
		       event.summary, event.location, event.start_at, event.description, event.online_meeting_json
		FROM calendar_events event
		JOIN calendar_sources source ON source.id = event.source_id
		JOIN accounts account ON account.id = source.account_id
		WHERE event.is_deleted = 0 AND event.all_day = 0
		  AND event.status <> 'cancelled' AND event.response_status <> 'declined'
		  AND source.user_id = event.user_id AND account.user_id = event.user_id
		  AND source.is_deleted = 0 AND source.is_selected = 1 AND source.is_hidden = 0
		  AND COALESCE(account.is_deleting, 0) = 0
		  AND event.start_at > ? AND event.start_at <= ?
		  AND NOT EXISTS (
		      SELECT 1 FROM calendar_reminders sent
		      WHERE sent.source_id = event.source_id AND sent.remote_id = event.remote_id AND sent.start_at = event.start_at)
		ORDER BY event.start_at, event.id
		LIMIT ?`,
		formatDBTime(now.UTC()), formatDBTime(now.Add(horizon).UTC()), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var reminders []CalendarReminder
	for rows.Next() {
		var r CalendarReminder
		var start sqliteNullTime
		if err := rows.Scan(&r.EventID, &r.UserID, &r.AccountID, &r.SourceID, &r.RemoteID, &r.Summary, &r.Location, &start, &r.Description, &r.OnlineMeetingJSON); err != nil {
			return nil, err
		}
		r.StartAt = start.Time
		reminders = append(reminders, r)
	}
	return reminders, rows.Err()
}

// ClaimCalendarReminder records that r is being announced. It reports false
// when another pass already did, so each occurrence is announced at most once,
// even across restarts. The claim comes before delivery on purpose: a crash in
// between drops one notification instead of repeating it.
func (db *DB) ClaimCalendarReminder(ctx context.Context, r CalendarReminder, now time.Time) (bool, error) {
	result, err := db.Write().ExecContext(ctx, `
		INSERT INTO calendar_reminders (source_id, remote_id, start_at, sent_at) VALUES (?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		r.SourceID, r.RemoteID, formatDBTime(r.StartAt), formatDBTime(now.UTC()))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// PruneCalendarReminders drops records for occurrences that began before cutoff.
func (db *DB) PruneCalendarReminders(ctx context.Context, cutoff time.Time) error {
	_, err := db.Write().ExecContext(ctx, `DELETE FROM calendar_reminders WHERE start_at < ?`, formatDBTime(cutoff.UTC()))
	return err
}
