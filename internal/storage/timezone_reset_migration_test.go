package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestMigrateV102ToV103ResetsStoredTimezoneOnce(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "gofer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()

	for id, stored := range map[string]string{
		"karachi": `{"timezone":"Asia/Karachi","theme":"light"}`,
		"unset":   `{"theme":"dark"}`,
	} {
		if _, err := db.Write().ExecContext(ctx, `INSERT INTO users (id, username, username_normalized, name) VALUES (?, ?, ?, ?)`, id, id, id, id); err != nil {
			t.Fatal(err)
		}
		if err := db.SetSetting(ctx, id, "ui_settings", stored); err != nil {
			t.Fatal(err)
		}
	}
	read := func(id string) map[string]string {
		var raw string
		if err := db.Read().QueryRowContext(ctx, `SELECT value FROM app_settings WHERE user_id = ? AND key = 'ui_settings'`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if _, err := db.Write().ExecContext(ctx, `UPDATE schema_version SET version = 102`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got := read("karachi"); got["timezone"] != "local" || got["theme"] != "light" {
		t.Errorf("karachi = %v, want timezone local and theme kept", got)
	}
	if got := read("unset"); got["timezone"] != "" {
		t.Errorf("unset gained a timezone: %v", got)
	}

	// Already at 103: a zone the user picks afterwards must survive a re-run.
	if err := db.SetSetting(ctx, "karachi", "ui_settings", `{"timezone":"Asia/Karachi"}`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if got := read("karachi"); got["timezone"] != "Asia/Karachi" {
		t.Errorf("migration ran twice: %v", got)
	}
}
