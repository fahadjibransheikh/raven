package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestMigrateV103ToV104HidesAvatarOnce(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "gofer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()

	custom := "railTop:avatar|header:from,date|meta:attachment|railMiddle:|body:subject|status:|railBottom:thread|footer:preview|corner:|hidden:labels"
	customWant := "railTop:|header:from,date|meta:attachment|railMiddle:|body:subject|status:|railBottom:thread|footer:preview|corner:|hidden:avatar,labels"
	cases := map[string]struct{ stored, layout, fields string }{
		"default": {`{"mail_card_layout":"` + v102DefaultMailCardLayout + `","mail_card_fields":"avatar,from,subject","theme":"light"}`, defaultMailCardLayout, "from,subject,accountMarker"},
		"custom":  {`{"mail_card_layout":"` + custom + `","mail_card_fields":"avatar,from,subject"}`, customWant, "from,subject"},
		"unset":   {`{"theme":"dark"}`, "", ""},
	}
	for id, tc := range cases {
		if _, err := db.Write().ExecContext(ctx, `INSERT INTO users (id, username, username_normalized, name) VALUES (?, ?, ?, ?)`, id, id, id, id); err != nil {
			t.Fatal(err)
		}
		if err := db.SetSetting(ctx, id, "ui_settings", tc.stored); err != nil {
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
	if _, err := db.Write().ExecContext(ctx, `UPDATE schema_version SET version = 103`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for id, tc := range cases {
		got := read(id)
		if got["mail_card_layout"] != tc.layout || got["mail_card_fields"] != tc.fields {
			t.Errorf("%s: layout=%q fields=%q, want %q / %q", id, got["mail_card_layout"], got["mail_card_fields"], tc.layout, tc.fields)
		}
	}
	if got := read("default")["theme"]; got != "light" {
		t.Errorf("other settings lost: theme=%q", got)
	}

	// Already at 104: an avatar the user drags back afterwards must survive a re-run.
	if err := db.SetSetting(ctx, "custom", "ui_settings", `{"mail_card_layout":"`+custom+`"}`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if got := read("custom")["mail_card_layout"]; got != custom {
		t.Errorf("migration ran twice: %q", got)
	}
}

func TestHideAvatarInMailCardLayoutIsIdempotent(t *testing.T) {
	for _, in := range []string{defaultMailCardLayout, v102DefaultMailCardLayout, "rail:avatar,thread|header:from|hidden:", "header:from"} {
		once := hideAvatarInMailCardLayout(in)
		if twice := hideAvatarInMailCardLayout(once); twice != once {
			t.Errorf("not idempotent: %q -> %q -> %q", in, once, twice)
		}
	}
	if got := hideAvatarInMailCardLayout("header:from"); got != "header:from|hidden:avatar" {
		t.Errorf("layout with no hidden zone = %q", got)
	}
}
