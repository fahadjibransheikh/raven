package storage

import (
	"database/sql"
	"fmt"
)

// Raven shipped its own calendar cache at v97 (calendars, calendar_events,
// calendar_account_state). Upstream gofer later built an incompatible calendar
// under the same calendar_events name; Raven now uses upstream's, created by
// migrateV107ToV108 onwards. See docs/upstream-merge-plan.md.

// migrateV96ToV97 used to create Raven's calendar tables. They are dropped again
// at v107, so a database still below v97 just records the version.
func migrateV96ToV97(tx *sql.Tx) error {
	return markSchemaVersion(tx, 97)
}

// ravenCalendarCarryoverTable keeps which calendars each account had in Raven's
// calendar, so the first calendar discovery after the upgrade can select them
// again instead of starting every account with no calendars.
const ravenCalendarCarryoverTable = `CREATE TABLE IF NOT EXISTS raven_calendar_carryover (
	account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	remote_id TEXT NOT NULL,
	selected INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (account_id, remote_id)
)`

// migrateV106ToV107 drops Raven's calendar cache so upstream's calendar schema
// can be created in its place. Events are only a provider cache (Raven wrote
// changes straight to the provider); the calendar list and selection are kept
// in raven_calendar_carryover.
func migrateV106ToV107(tx *sql.Tx) error {
	if _, err := tx.Exec(ravenCalendarCarryoverTable); err != nil {
		return fmt.Errorf("create calendar carryover: %w", err)
	}
	hasCalendars, err := tableExistsTx(tx, "calendars")
	if err != nil {
		return fmt.Errorf("check Raven calendars: %w", err)
	}
	if hasCalendars {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO raven_calendar_carryover (account_id, remote_id, selected)
			SELECT account_id, provider_calendar_id, MAX(selected) FROM calendars
			WHERE account_id IN (SELECT id FROM accounts)
			GROUP BY account_id, provider_calendar_id`); err != nil {
			return fmt.Errorf("keep Raven calendar selection: %w", err)
		}
	}
	for _, statement := range []string{
		`DROP TABLE IF EXISTS calendar_events`,
		`DROP TABLE IF EXISTS calendars`,
		`DROP TABLE IF EXISTS calendar_account_state`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("drop Raven calendar cache: %w", err)
		}
	}
	return markSchemaVersion(tx, 107)
}
