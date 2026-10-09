package storage

import (
	"context"
	"fmt"
)

// migrateV104ToV105 adds the List-Unsubscribe / List-Unsubscribe-Post header values
// captured when a message body is parsed. Existing rows stay empty (no backfill).
func (db *DB) migrateV104ToV105(ctx context.Context) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin list-unsubscribe migration: %w", err)
	}
	defer tx.Rollback()

	hasMessages, err := tableExistsTx(tx, "messages")
	if err != nil {
		return err
	}
	for _, column := range []string{"list_unsubscribe", "list_unsubscribe_post"} {
		if !hasMessages {
			break
		}
		exists, err := columnExistsTx(tx, "messages", column)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := tx.ExecContext(ctx, `ALTER TABLE messages ADD COLUMN `+column+` TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add messages.%s: %w", column, err)
		}
	}
	if err := markSchemaVersion(tx, 105); err != nil {
		return fmt.Errorf("mark schema version 105: %w", err)
	}
	return tx.Commit()
}
