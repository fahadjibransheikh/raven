package storage

import (
	"database/sql"
	"fmt"
)

// migrateV95ToV96 adds per-account sending identities and gives every existing
// account its primary identity. Safe to re-run: every step is conditional.
func migrateV95ToV96(tx *sql.Tx) error {
	hasAccounts, err := tableExistsTx(tx, "accounts")
	if err != nil {
		return fmt.Errorf("check accounts table: %w", err)
	}
	if !hasAccounts {
		// Partial legacy fixtures: a foreign key to a missing parent would break them.
		return markSchemaVersion(tx, 96)
	}
	for _, column := range []struct{ name, ddl string }{
		{"identities_synced_at", `ALTER TABLE accounts ADD COLUMN identities_synced_at DATETIME`},
		{"identity_default_pinned", `ALTER TABLE accounts ADD COLUMN identity_default_pinned INTEGER NOT NULL DEFAULT 0`},
	} {
		exists, err := columnExistsTx(tx, "accounts", column.name)
		if err != nil {
			return fmt.Errorf("check accounts.%s: %w", column.name, err)
		}
		if !exists {
			if _, err := tx.Exec(column.ddl); err != nil {
				return fmt.Errorf("add accounts.%s: %w", column.name, err)
			}
		}
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS account_identities (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
			email TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL CHECK (source IN ('primary', 'provider', 'manual')),
			is_default INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(account_id, email)
		)`,
		// Exactly-one-default is enforced here as well as in code.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_account_identities_default
		 ON account_identities(account_id) WHERE is_default = 1`,
		`CREATE TABLE IF NOT EXISTS account_identity_dismissals (
			account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
			email TEXT NOT NULL,
			dismissed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (account_id, email)
		)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("account identities: %w", err)
		}
	}
	// Very old fixtures predate these columns; they have nothing to back-fill.
	hasEmail, err := columnExistsTx(tx, "accounts", "email_address")
	if err != nil {
		return fmt.Errorf("check accounts.email_address: %w", err)
	}
	if hasEmail {
		nameExpr := "''"
		if hasName, err := columnExistsTx(tx, "accounts", "display_name"); err != nil {
			return fmt.Errorf("check accounts.display_name: %w", err)
		} else if hasName {
			nameExpr = "COALESCE(display_name, '')"
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO account_identities (account_id, email, name, source, is_default)
			SELECT id, lower(trim(email_address)), ` + nameExpr + `, 'primary', 1
			FROM accounts WHERE trim(email_address) != ''`); err != nil {
			return fmt.Errorf("back-fill primary identities: %w", err)
		}
	}
	return markSchemaVersion(tx, 96)
}
