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

// migrateV106ToV107 drops Raven's calendar cache so upstream's calendar schema
// can be created in its place. Only cached provider data is lost: Raven wrote
// calendar changes straight to the provider, so the calendar re-syncs.
func migrateV106ToV107(tx *sql.Tx) error {
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
