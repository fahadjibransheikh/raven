package storage

import (
	"testing"
	"time"

	avatarresolver "github.com/fahadjibransheikh/raven/internal/avatar"
)

func TestGetGmailAccountIDByEmail(t *testing.T) {
	db := newContactsTestDB(t)
	ctx := t.Context()
	for _, row := range []struct{ id, provider, email string }{
		{"gmail-live", "gmail", "Owner@Brand.example"},
		{"imap", "imap", "mail@brand.example"},
	} {
		if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, provider, email_address) VALUES (?, ?, ?)`, row.id, row.provider, row.email); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, provider, email_address, is_deleting) VALUES ('gmail-gone', 'gmail', 'gone@brand.example', 1)`); err != nil {
		t.Fatal(err)
	}
	for email, want := range map[string]string{
		" owner@brand.example ": "gmail-live",
		"mail@brand.example":    "",
		"gone@brand.example":    "",
		"other@brand.example":   "",
	} {
		if got, err := db.GetGmailAccountIDByEmail(ctx, email); err != nil || got != want {
			t.Errorf("GetGmailAccountIDByEmail(%q) = (%q, %v), want %q", email, got, err, want)
		}
	}
}

func TestExtendSenderAvatarKeepsImageOfSourceOnly(t *testing.T) {
	db := newContactsTestDB(t)
	ctx := t.Context()
	email := "owner@brand.example"
	hash := avatarresolver.GravatarHash(email)
	if err := db.SaveSenderAvatarFound(ctx, hash, email, "google_profile", "image/png", "", []byte("png"), time.Now().Add(-time.Minute), "unchecked", "unchecked"); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.ExtendSenderAvatar(ctx, hash, "domain_icon", time.Now().Add(time.Hour)); err != nil || ok {
		t.Fatalf("ExtendSenderAvatar() for another source = (%v, %v), want no change", ok, err)
	}
	until := time.Now().Add(6 * time.Hour)
	if ok, err := db.ExtendSenderAvatar(ctx, hash, "google_profile", until); err != nil || !ok {
		t.Fatalf("ExtendSenderAvatar() = (%v, %v), want extended", ok, err)
	}
	rec, err := db.GetSenderAvatarByHash(ctx, hash)
	if err != nil || rec == nil || string(rec.ImageData) != "png" || rec.Status != "found" || rec.ExpiresAt.Sub(until).Abs() > time.Second {
		t.Fatalf("record = (%+v, %v), want the same image expiring at %v", rec, err, until)
	}
}
