package storage

import (
	"database/sql"
	"fmt"
)

// migrateV94ToV95 adds the whole-folder "mark all as read" queue. It is a
// sibling of message_mutations because message_mutations rows are keyed by
// message_id (NOT NULL, cascade) and a folder-level job has no message.
func migrateV94ToV95(tx *sql.Tx) error {
	// Partial legacy fixtures have no folders table; a foreign key to a missing
	// parent would break their account deletes.
	hasFolders, err := tableExistsTx(tx, "folders")
	if err != nil {
		return fmt.Errorf("check folders table: %w", err)
	}
	if !hasFolders {
		return markSchemaVersion(tx, 95)
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS folder_read_mutations (
			id TEXT PRIMARY KEY,
			account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
			folder_id TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
			provider_type TEXT NOT NULL CHECK (provider_type IN ('gmail', 'outlook', 'imap')),
			cutoff_at DATETIME NOT NULL,
			cutoff_message_id INTEGER NOT NULL DEFAULT 0,
			max_uid INTEGER NOT NULL DEFAULT 0,
			uid_validity INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'failed')),
			attempt_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			locked_at DATETIME,
			next_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(folder_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_folder_read_mutations_due
		 ON folder_read_mutations(status, next_attempt_at, created_at)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("create folder read queue: %w", err)
		}
	}
	return markSchemaVersion(tx, 95)
}
