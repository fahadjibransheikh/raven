package storage

import (
	"path/filepath"
	"testing"
)

func TestMigrateV104ToV105AddsListUnsubscribeColumns(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "gofer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()

	// Simulate a v104 database: columns absent.
	for _, c := range []string{"list_unsubscribe", "list_unsubscribe_post"} {
		if _, err := db.Write().ExecContext(ctx, `ALTER TABLE messages DROP COLUMN `+c); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Write().ExecContext(ctx, `UPDATE schema_version SET version = 104`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, c := range []string{"list_unsubscribe", "list_unsubscribe_post"} {
		if _, err := db.Read().ExecContext(ctx, `SELECT `+c+` FROM messages LIMIT 1`); err != nil {
			t.Errorf("column %s missing: %v", c, err)
		}
	}
	var version int
	if err := db.Read().QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil || version != 106 {
		t.Errorf("version = %d, %v; want 106", version, err)
	}
	if err := db.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}
