package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestMigrateV100ToV101ResetsOnlyTheOldDefaultMailCardLayout(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "gofer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()

	// Migrating from 100 chains through 101 into 102, so an untouched layout
	// ends on the current default.
	newDefault := defaultUISettings()["mail_card_layout"]
	if newDefault == oldDefaultMailCardLayout {
		t.Fatal("default layout was not changed")
	}
	custom := "railTop:avatar|header:from,date|meta:|railMiddle:|body:subject|status:|railBottom:thread|footer:|corner:|hidden:preview"
	cases := map[string]struct{ stored, want string }{
		"old":    {`{"mail_card_layout":"` + oldDefaultMailCardLayout + `","theme":"light"}`, newDefault},
		"custom": {`{"mail_card_layout":"` + custom + `"}`, hideAvatarInMailCardLayout(custom)},
		"unset":  {`{"theme":"dark"}`, ""},
	}
	for id, tc := range cases {
		if _, err := db.Write().ExecContext(ctx, `INSERT INTO users (id, username, username_normalized, name) VALUES (?, ?, ?, ?)`, id, id, id, id); err != nil {
			t.Fatal(err)
		}
		if err := db.SetSetting(ctx, id, "ui_settings", tc.stored); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Write().ExecContext(ctx, `UPDATE schema_version SET version = 100`); err != nil {
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
		if got["mail_card_layout"] != tc.want {
			t.Errorf("%s: layout = %q, want %q", id, got["mail_card_layout"], tc.want)
		}
	}
	var raw string
	_ = db.Read().QueryRowContext(ctx, `SELECT value FROM app_settings WHERE user_id = 'old'`).Scan(&raw)
	var m map[string]string
	_ = json.Unmarshal([]byte(raw), &m)
	if m["theme"] != "light" {
		t.Errorf("other settings lost: %s", raw)
	}
	var version int
	_ = db.Read().QueryRowContext(ctx, `SELECT MAX(version) FROM schema_version`).Scan(&version)
	if version != CurrentSchemaVersion {
		t.Errorf("version = %d, want %d", version, CurrentSchemaVersion)
	}
}

func TestMigrateV101ToV102ResetsOnlyTheV101DefaultMailCardLayout(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "gofer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()

	if v101DefaultMailCardLayout == defaultMailCardLayout {
		t.Fatal("default layout was not changed")
	}
	if got := defaultUISettings()["mail_card_layout"]; got != defaultMailCardLayout {
		t.Fatalf("defaultUISettings layout = %q, want the calm default", got)
	}
	custom := "railTop:avatar|header:from,date|meta:|railMiddle:|body:subject|status:|railBottom:thread|footer:preview|corner:|hidden:labels"
	cases := map[string]struct{ stored, want string }{
		"v101":   {`{"mail_card_layout":"` + v101DefaultMailCardLayout + `","theme":"light"}`, defaultMailCardLayout},
		"custom": {`{"mail_card_layout":"` + custom + `"}`, hideAvatarInMailCardLayout(custom)},
		"unset":  {`{"theme":"dark"}`, ""},
	}
	for id, tc := range cases {
		if _, err := db.Write().ExecContext(ctx, `INSERT INTO users (id, username, username_normalized, name) VALUES (?, ?, ?, ?)`, id, id, id, id); err != nil {
			t.Fatal(err)
		}
		if err := db.SetSetting(ctx, id, "ui_settings", tc.stored); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Write().ExecContext(ctx, `UPDATE schema_version SET version = 101`); err != nil {
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
		if got["mail_card_layout"] != tc.want {
			t.Errorf("%s: layout = %q, want %q", id, got["mail_card_layout"], tc.want)
		}
		if id == "v101" && got["theme"] != "light" {
			t.Errorf("other settings lost: %s", raw)
		}
	}
	var version int
	_ = db.Read().QueryRowContext(ctx, `SELECT MAX(version) FROM schema_version`).Scan(&version)
	if version != 105 {
		t.Errorf("version = %d, want 105", version)
	}
}

func TestMailListDensityDefaultsToCalm(t *testing.T) {
	if got := defaultUISettings()["mail_list_density"]; got != "calm" {
		t.Errorf("mail_list_density default = %q, want calm", got)
	}
}
