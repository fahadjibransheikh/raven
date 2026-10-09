package storage

import (
	"path/filepath"
	"testing"
)

func TestMigrateV105ToV106AddsDeliveredTo(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "gofer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	if _, err := db.Write().ExecContext(ctx, `ALTER TABLE messages DROP COLUMN delivered_to`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().ExecContext(ctx, `UPDATE schema_version SET version = 105`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.Read().ExecContext(ctx, `SELECT delivered_to FROM messages LIMIT 1`); err != nil {
		t.Errorf("column missing: %v", err)
	}
	var version int
	if err := db.Read().QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil || version != 106 {
		t.Errorf("version = %d, %v; want 106", version, err)
	}
	if err := db.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}
