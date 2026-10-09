package storage

import (
	"context"
	"fmt"
)

// migrateV105ToV106 adds messages.delivered_to: the comma-joined Delivered-To and
// X-Original-To addresses captured at sync, used to pick the alias a message reached
// us through. Existing rows stay empty (no backfill).
func (db *DB) migrateV105ToV106(ctx context.Context) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delivered-to migration: %w", err)
	}
	defer tx.Rollback()

	hasMessages, err := tableExistsTx(tx, "messages")
	if err != nil {
		return err
	}
	if hasMessages {
		exists, err := columnExistsTx(tx, "messages", "delivered_to")
		if err != nil {
			return err
		}
		if !exists {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE messages ADD COLUMN delivered_to TEXT NOT NULL DEFAULT ''`); err != nil {
				return fmt.Errorf("add messages.delivered_to: %w", err)
			}
		}
	}
	if err := markSchemaVersion(tx, 106); err != nil {
		return fmt.Errorf("mark schema version 106: %w", err)
	}
	return tx.Commit()
}
