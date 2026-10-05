package storage

import (
	"database/sql"
	"fmt"
)

// migrateV96ToV97 adds the calendar tables (read-only Google sync, phase C1a).
// Safe to re-run: every statement is conditional.
func migrateV96ToV97(tx *sql.Tx) error {
	hasAccounts, err := tableExistsTx(tx, "accounts")
	if err != nil {
		return fmt.Errorf("check accounts table: %w", err)
	}
	if !hasAccounts {
		// Partial legacy fixtures: a foreign key to a missing parent would break them.
		return markSchemaVersion(tx, 97)
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS calendars (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
			provider_calendar_id TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			color TEXT NOT NULL DEFAULT '',
			time_zone TEXT NOT NULL DEFAULT '',
			is_primary INTEGER NOT NULL DEFAULT 0,
			access_role TEXT NOT NULL DEFAULT '',
			selected INTEGER NOT NULL DEFAULT 0,
			sync_token TEXT NOT NULL DEFAULT '',
			window_start DATETIME,
			window_end DATETIME,
			synced_at DATETIME,
			UNIQUE(account_id, provider_calendar_id)
		)`,
		`CREATE TABLE IF NOT EXISTS calendar_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			calendar_id INTEGER NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
			account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
			provider_event_id TEXT NOT NULL,
			ical_uid TEXT NOT NULL DEFAULT '',
			recurring_event_id TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			location TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			start_at DATETIME NOT NULL,
			end_at DATETIME NOT NULL,
			all_day INTEGER NOT NULL DEFAULT 0,
			start_date TEXT NOT NULL DEFAULT '',
			end_date TEXT NOT NULL DEFAULT '',
			event_time_zone TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'confirmed',
			organizer_email TEXT NOT NULL DEFAULT '',
			self_response TEXT NOT NULL DEFAULT '',
			attendees TEXT NOT NULL DEFAULT '[]',
			html_link TEXT NOT NULL DEFAULT '',
			meeting_url TEXT NOT NULL DEFAULT '',
			updated_at_provider DATETIME,
			UNIQUE(calendar_id, provider_event_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_calendar_events_account_start ON calendar_events(account_id, start_at)`,
		`CREATE INDEX IF NOT EXISTS idx_calendar_events_calendar_start ON calendar_events(calendar_id, start_at)`,
		// Per-account (not per-calendar) because a missing OAuth scope blocks every calendar.
		`CREATE TABLE IF NOT EXISTS calendar_account_state (
			account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
			needs_reconnect INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			last_synced_at DATETIME
		)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("calendar schema: %w", err)
		}
	}
	return markSchemaVersion(tx, 97)
}
