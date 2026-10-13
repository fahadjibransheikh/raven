package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// Tables that exist only in the per-user layout are versioned with SQLite's
// user_version, separately from the shared schema_version every database
// carries. Each step runs once, in order, in the transaction that records its
// version. Step 1 is the layout's original tables; it creates them if missing,
// so earlier layouts adopt the version. Later steps hold actual changes.
type layoutSchemaStep func(context.Context, *sql.Tx) error

// raven_mailbox_credentials is not part of these steps: mailauth creates it at
// startup, and the migration's credential import requires it to be absent
// until then. Steps must not reference it.
//
// Step 2 follows the rename from the project Raven began as. Before any step
// runs, applyLayoutSchemaTx renames the old-prefixed tables and drops their
// old-named indexes and triggers (legacy_rename.go); step 2 creates them again
// under the new names. Both steps are the same create-if-missing statements, so
// a new database ends up in the same place either way.
func centralLayoutStep(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, userStoreDirectorySchema+";"+accountRoutingSchema)
	return err
}

func userLayoutStep(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, providerSendReceiptSchema); err != nil {
		return err
	}
	return ensureUserProviderDraftSchema(ctx, tx)
}

var centralLayoutSchema = []layoutSchemaStep{centralLayoutStep, centralLayoutStep}

var userLayoutSchema = []layoutSchemaStep{userLayoutStep, userLayoutStep}

// ensureLayoutSchema only reads the version when the database is current.
func ensureLayoutSchema(ctx context.Context, db *DB, steps []layoutSchemaStep) error {
	var version int
	if err := db.Read().QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version == len(steps) {
		return nil
	}
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := applyLayoutSchemaTx(ctx, tx, steps); err != nil {
		return err
	}
	return tx.Commit()
}

func applyLayoutSchemaTx(ctx context.Context, tx *sql.Tx, steps []layoutSchemaStep) error {
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > len(steps) {
		return fmt.Errorf("layout schema version %d was written by a newer Raven release; this release supports %d", version, len(steps))
	}
	if version == len(steps) {
		return nil
	}
	if err := renameLegacyLayoutObjects(ctx, tx); err != nil {
		return err
	}
	for _, step := range steps[version:] {
		if err := step(ctx, tx); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version=%d`, len(steps)))
	return err
}
