package storage

import (
	"path/filepath"
	"testing"
)

func TestRemoteImagesDefaultIsBlockedForNewUsersButNotExistingOnes(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "gofer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()

	stored := map[string]string{
		"norow":    "", // never saved settings: effective value was the old default, true
		"nokey":    `{"theme":"light"}`,
		"emptykey": `{"load_remote_images":""}`,
		"offf":     `{"load_remote_images":"false"}`,
		"onn":      `{"load_remote_images":"true"}`,
		"broken":   `{not json`,
	}
	for id, v := range stored {
		if _, err := db.Write().ExecContext(ctx, `INSERT INTO users (id, username, username_normalized, name) VALUES (?, ?, ?, ?)`, id, id, id, id); err != nil {
			t.Fatal(err)
		}
		if v != "" {
			if err := db.SetSetting(ctx, id, "ui_settings", v); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Write().ExecContext(ctx, `UPDATE schema_version SET version = 99`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	want := map[string]string{"norow": "true", "nokey": "true", "emptykey": "true", "offf": "false", "onn": "true", "broken": "false"}
	for id, w := range want {
		if got := db.GetUISettings(ctx, id)["load_remote_images"]; got != w {
			t.Errorf("%s: load_remote_images = %q, want %q", id, got, w)
		}
	}
	if got := db.GetUISettings(ctx, "nokey")["theme"]; got != "light" {
		t.Errorf("other settings lost: theme = %q", got)
	}

	// A user (or account) created after the migration gets the blocked default.
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO users (id, username, username_normalized, name) VALUES ('fresh','fresh','fresh','fresh')`); err != nil {
		t.Fatal(err)
	}
	if got := db.GetUISettings(ctx, "fresh")["load_remote_images"]; got != "false" {
		t.Errorf("new user load_remote_images = %q, want false", got)
	}
	if got := defaultUISettings()["load_remote_images"]; got != "false" {
		t.Errorf("default = %q, want false", got)
	}
}
