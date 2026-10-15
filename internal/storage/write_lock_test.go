package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// Another process (Litestream replicating the file, a CLI) can hold SQLite's
// write lock. A transaction that reads first and then writes must wait for it
// within busy_timeout; with a deferred BEGIN the upgrade fails at once with
// SQLITE_BUSY, because the busy handler is not invoked for a connection that
// already holds a read snapshot.
func TestWritePoolWaitsForAnotherWriter(t *testing.T) {
	ctx := context.Background()
	db, err := New(filepath.Join(t.TempDir(), "writer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Write().ExecContext(ctx, `CREATE TABLE lock_probe (id INTEGER PRIMARY KEY, note TEXT)`); err != nil {
		t.Fatal(err)
	}

	// A second handle on the same file stands in for the other process.
	other, err := openDB(db.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	const held = 300 * time.Millisecond
	time.AfterFunc(held, func() { _, _ = conn.ExecContext(ctx, `COMMIT`) })

	start := time.Now()
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM lock_probe`).Scan(&existing); err != nil {
		t.Fatalf("read before write: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO lock_probe (note) VALUES ('x')`); err != nil {
		t.Fatalf("write after read (waited %s): %v", time.Since(start), err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if waited := time.Since(start); waited < held/2 {
		t.Fatalf("write finished after %s without waiting for the other writer", waited)
	}
}

// Readers stay deferred: they must not queue behind a writer.
func TestReadPoolTransactionsDoNotTakeWriteLock(t *testing.T) {
	ctx := context.Background()
	db, err := New(filepath.Join(t.TempDir(), "reader.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	other, err := openDB(db.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, `ROLLBACK`)

	start := time.Now()
	tx, err := db.Read().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_version`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(start); waited > time.Second {
		t.Fatalf("read transaction waited %s for a writer", waited)
	}
}
