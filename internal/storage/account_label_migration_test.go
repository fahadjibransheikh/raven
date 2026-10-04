package storage

import (
	"path/filepath"
	"testing"
)

func TestMigrateV93ToV94AddsAccountLabelPreservingRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gofer.db")
	raw, err := openDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE schema_version (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO schema_version (version) VALUES (93);
		CREATE TABLE accounts (
			id TEXT PRIMARY KEY,
			email_address TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO accounts (id, email_address, display_name) VALUES ('a1', 'a@example.com', 'Fahad Sheikh')`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var version int
	if err := db.Read().QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != CurrentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, CurrentSchemaVersion)
	}
	var name, label string
	if err := db.Read().QueryRow(`SELECT display_name, label FROM accounts WHERE id = 'a1'`).Scan(&name, &label); err != nil {
		t.Fatal(err)
	}
	if name != "Fahad Sheikh" || label != "" {
		t.Fatalf("migrated row = name:%q label:%q, want original name and empty label", name, label)
	}
}

func TestMigrateV93ToV94IsIdempotentWhenColumnExists(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "gofer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.Write().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := migrateV93ToV94(tx); err != nil {
		t.Fatalf("migrateV93ToV94 on fresh schema: %v", err)
	}
}
