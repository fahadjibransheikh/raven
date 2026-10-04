package storage

import (
	"database/sql"
	"fmt"
)

// migrateV93ToV94 adds the display-only account label. It never feeds the From
// header: accounts.display_name remains the outgoing sender name.
func migrateV93ToV94(tx *sql.Tx) error {
	hasAccounts, err := tableExistsTx(tx, "accounts")
	if err != nil {
		return fmt.Errorf("check accounts table: %w", err)
	}
	if hasAccounts {
		exists, err := columnExistsTx(tx, "accounts", "label")
		if err != nil {
			return fmt.Errorf("check account label column: %w", err)
		}
		if !exists {
			if _, err := tx.Exec(`ALTER TABLE accounts ADD COLUMN label TEXT NOT NULL DEFAULT ''`); err != nil {
				return fmt.Errorf("add account label column: %w", err)
			}
		}
	}
	return markSchemaVersion(tx, 94)
}
