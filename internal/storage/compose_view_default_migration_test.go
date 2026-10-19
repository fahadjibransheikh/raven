package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestMigrateV123ToV124MovesOnlyStoredDialogComposeToInline(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "raven.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()

	cases := map[string]struct {
		stored string
		want   map[string]string
	}{
		"dialog": {`{"default_compose_view":"dialog","default_new_compose_view":"dialog","default_reply_compose_view":"dialog","theme":"light"}`,
			map[string]string{"default_compose_view": "pane", "default_new_compose_view": "pane", "default_reply_compose_view": "pane", "theme": "light"}},
		"mixed": {`{"default_new_compose_view":"full","default_reply_compose_view":"dialog"}`,
			map[string]string{"default_new_compose_view": "full", "default_reply_compose_view": "pane"}},
		"legacy": {`{"default_compose_view":"dialog"}`, map[string]string{"default_compose_view": "pane"}},
		"chosen": {`{"default_new_compose_view":"full","default_reply_compose_view":"pane"}`,
			map[string]string{"default_new_compose_view": "full", "default_reply_compose_view": "pane"}},
		"unset": {`{"theme":"dark"}`, map[string]string{"theme": "dark", "default_new_compose_view": ""}},
	}
	for id, tc := range cases {
		if _, err := db.Write().ExecContext(ctx, `INSERT INTO users (id, username, username_normalized, name) VALUES (?, ?, ?, ?)`, id, id, id, id); err != nil {
			t.Fatal(err)
		}
		if err := db.SetSetting(ctx, id, "ui_settings", tc.stored); err != nil {
			t.Fatal(err)
		}
	}
	// Unparseable JSON must be left untouched, not abort the migration.
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO users (id, username, username_normalized, name) VALUES ('broken','broken','broken','broken')`); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(ctx, "broken", "ui_settings", `{not json`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().ExecContext(ctx, `UPDATE schema_version SET version = 123`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for id, tc := range cases {
		var raw string
		if err := db.Read().QueryRowContext(ctx, `SELECT value FROM app_settings WHERE user_id = ? AND key = 'ui_settings'`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var got map[string]string
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		for key, want := range tc.want {
			if got[key] != want {
				t.Errorf("%s: %s = %q, want %q (stored %s)", id, key, got[key], want, raw)
			}
		}
	}
	var broken string
	_ = db.Read().QueryRowContext(ctx, `SELECT value FROM app_settings WHERE user_id = 'broken'`).Scan(&broken)
	if broken != `{not json` {
		t.Fatalf("broken row changed: %s", broken)
	}

	// A later boot must not touch a choice made after the migration.
	if err := db.SetSetting(ctx, "dialog", "ui_settings", `{"default_new_compose_view":"dialog"}`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(); err != nil {
		t.Fatal(err)
	}
	var raw string
	_ = db.Read().QueryRowContext(ctx, `SELECT value FROM app_settings WHERE user_id = 'dialog'`).Scan(&raw)
	if raw != `{"default_new_compose_view":"dialog"}` {
		t.Fatalf("re-run changed a post-migration choice: %s", raw)
	}
}

func TestNewUsersDefaultToInlineCompose(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "raven.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	settings := db.GetUISettings(t.Context(), "nobody")
	for _, key := range []string{"default_new_compose_view", "default_reply_compose_view"} {
		if settings[key] != "pane" {
			t.Errorf("%s = %q, want pane", key, settings[key])
		}
	}
}
