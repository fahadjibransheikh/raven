package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestMigrateV98ToV99ResetsOnlyTheOldDefaultMailListWidth(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "gofer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()

	cases := map[string]struct{ stored, want string }{
		"old":        {`{"mail_list_width":"50%","theme":"light"}`, "30%"},
		"decimal":    {`{"mail_list_width":"50.0%"}`, "30%"},
		"spaced":     {`{"mail_list_width":" 50 %"}`, "30%"},
		"custom":     {`{"mail_list_width":"42.5%"}`, "42.5%"},
		"pixels":     {`{"mail_list_width":"500px"}`, "500px"},
		"bare50":     {`{"mail_list_width":"50"}`, "50"},
		"already":    {`{"mail_list_width":"30%"}`, "30%"},
		"unset":      {`{"theme":"dark"}`, ""},
		"not-string": {`{"mail_list_width":50}`, ""},
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
	if _, err := db.Write().ExecContext(ctx, `UPDATE schema_version SET version = 98`); err != nil {
		t.Fatal(err)
	}

	for run := 0; run < 2; run++ { // second run: a later boot must not touch a user's new choice
		if run == 1 {
			if err := db.SetSetting(ctx, "old", "ui_settings", `{"mail_list_width":"50%"}`); err != nil {
				t.Fatal(err)
			}
			if err := db.migrate(); err != nil { // version is current now: no-op
				t.Fatal(err)
			}
			var raw string
			_ = db.Read().QueryRowContext(ctx, `SELECT value FROM app_settings WHERE user_id = 'old'`).Scan(&raw)
			if raw != `{"mail_list_width":"50%"}` {
				t.Fatalf("re-run changed a post-migration choice: %s", raw)
			}
			return
		}
		if err := db.migrate(); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		for id, tc := range cases {
			var raw string
			if err := db.Read().QueryRowContext(ctx, `SELECT value FROM app_settings WHERE user_id = ? AND key = 'ui_settings'`, id).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &got); err != nil {
				t.Fatalf("%s: %v", id, err)
			}
			var w string
			_ = json.Unmarshal(got["mail_list_width"], &w)
			if w != tc.want {
				t.Errorf("%s: mail_list_width = %q, want %q (stored %s)", id, w, tc.want, raw)
			}
		}
		var theme string
		var raw string
		_ = db.Read().QueryRowContext(ctx, `SELECT value FROM app_settings WHERE user_id = 'old'`).Scan(&raw)
		var m map[string]string
		_ = json.Unmarshal([]byte(raw), &m)
		theme = m["theme"]
		if theme != "light" {
			t.Errorf("other settings lost: %s", raw)
		}
		var broken string
		_ = db.Read().QueryRowContext(ctx, `SELECT value FROM app_settings WHERE user_id = 'broken'`).Scan(&broken)
		if broken != `{not json` {
			t.Errorf("unparseable row modified: %q", broken)
		}
		var version int
		_ = db.Read().QueryRowContext(ctx, `SELECT MAX(version) FROM schema_version`).Scan(&version)
		if version != 105 {
			t.Errorf("version = %d, want 105", version)
		}
	}
}
