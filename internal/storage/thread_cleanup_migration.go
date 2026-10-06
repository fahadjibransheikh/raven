package storage

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

const (
	orphanThreadBatchRowids = 250000
	// VACUUM only pays off when a meaningful share of the file is free pages.
	vacuumMinFreePages = 10000
	vacuumMinFreeRatio = 0.2
)

// migrateV97ToV98 deletes thread rows that no message references. Before the
// reconcile fix every re-upsert of a root message orphaned its thread, leaving
// millions of dead rows. Safe to re-run. It runs in rowid-range batches, each
// in its own transaction, so a multi-million-row table does not produce one
// multi-GB WAL; the schema version is marked last, so an interrupted run
// simply resumes.
func (db *DB) migrateV97ToV98(ctx context.Context) (int64, error) {
	conn, err := db.write.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()

	hasThreads, err := connTableExists(ctx, conn, "threads")
	if err != nil {
		return 0, err
	}
	hasMessages, err := connTableExists(ctx, conn, "messages")
	if err != nil {
		return 0, err
	}
	var deleted int64
	if hasThreads && hasMessages {
		// idx_messages_thread leads with account_id, so a thread_id lookup
		// against messages cannot use it; build the live set once instead.
		for _, stmt := range []string{
			`DROP TABLE IF EXISTS temp.live_threads`,
			`CREATE TEMP TABLE live_threads AS
			   SELECT DISTINCT thread_id AS id FROM messages WHERE thread_id IS NOT NULL AND thread_id != ''`,
			`CREATE UNIQUE INDEX temp.live_threads_id ON live_threads(id)`,
		} {
			if _, err := conn.ExecContext(ctx, stmt); err != nil {
				return 0, fmt.Errorf("prepare live thread set: %w", err)
			}
		}
		defer func() { _, _ = conn.ExecContext(ctx, `DROP TABLE IF EXISTS temp.live_threads`) }()

		var minRowid, maxRowid sql.NullInt64
		if err := conn.QueryRowContext(ctx, `SELECT MIN(rowid), MAX(rowid) FROM threads`).Scan(&minRowid, &maxRowid); err != nil {
			return 0, fmt.Errorf("read thread rowid range: %w", err)
		}
		for lo := minRowid.Int64; minRowid.Valid && lo <= maxRowid.Int64; lo += orphanThreadBatchRowids {
			tx, err := conn.BeginTx(ctx, nil)
			if err != nil {
				return deleted, fmt.Errorf("begin orphan thread batch: %w", err)
			}
			res, err := tx.ExecContext(ctx,
				`DELETE FROM threads WHERE rowid >= ? AND rowid < ? AND id NOT IN (SELECT id FROM temp.live_threads)`,
				lo, lo+orphanThreadBatchRowids)
			if err != nil {
				_ = tx.Rollback()
				return deleted, fmt.Errorf("delete orphan threads: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return deleted, fmt.Errorf("commit orphan thread batch: %w", err)
			}
			n, _ := res.RowsAffected()
			deleted += n
		}
	}
	if _, err := conn.ExecContext(ctx, `INSERT OR REPLACE INTO schema_version (version) VALUES (98)`); err != nil {
		return deleted, fmt.Errorf("mark schema version 98: %w", err)
	}
	log.Printf("storage: removed %d orphan thread rows", deleted)
	return deleted, nil
}

func connTableExists(ctx context.Context, conn *sql.Conn, name string) (bool, error) {
	var n int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		return false, fmt.Errorf("check %s table: %w", name, err)
	}
	return n > 0, nil
}

// vacuumIfWorthwhile reclaims free pages left by a bulk delete. VACUUM cannot
// run inside a transaction, so this must be called with no migration tx open.
func (db *DB) vacuumIfWorthwhile(ctx context.Context) error {
	var free, total int64
	if err := db.write.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
		return err
	}
	if err := db.write.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&total); err != nil {
		return err
	}
	if free < vacuumMinFreePages || total == 0 || float64(free)/float64(total) < vacuumMinFreeRatio {
		return nil
	}
	log.Printf("storage: vacuuming (%d of %d pages free)", free, total)
	_, err := db.write.ExecContext(ctx, `VACUUM`)
	return err
}
