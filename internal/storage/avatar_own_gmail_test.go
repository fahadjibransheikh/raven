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

func TestGetOwnGmailAvatarCandidates(t *testing.T) {
	db := newContactsTestDB(t)
	ctx := t.Context()
	for _, email := range []string{"icon@brand.example", "photo@brand.example", "none@brand.example", "failed@brand.example", "retry@brand.example", "gone@brand.example", "noimage@brand.example"} {
		if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, provider, email_address, is_deleting) VALUES (?, 'gmail', ?, ?)`, email, email, email == "gone@brand.example"); err != nil {
			t.Fatal(err)
		}
	}
	save := func(email, source string) string {
		hash := avatarresolver.GravatarHash(email)
		if err := db.SaveSenderAvatarFound(ctx, hash, email, source, "image/png", "", []byte("png"), time.Now().Add(time.Hour), "unchecked", "unchecked"); err != nil {
			t.Fatal(err)
		}
		return hash
	}
	save("icon@brand.example", "domain_icon")
	save("photo@brand.example", "google_profile")
	save("stranger@brand.example", "domain_icon")
	save("gone@brand.example", "domain_icon")
	for email, status := range map[string]string{"none@brand.example": "missing", "failed@brand.example": "error", "retry@brand.example": "error"} {
		hash := save(email, "domain_icon")
		if err := db.RecordSenderAvatarAttempt(ctx, hash, email, "google_profile", status, "attempt"); err != nil {
			t.Fatal(err)
		}
	}
	// No provider found an image for this mailbox; it waits for a retry days away.
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO sender_avatars (email_hash, email, source, status, next_retry_at) VALUES (?, 'noimage@brand.example', 'libravatar', 'error', datetime('now', '+2 days'))`, avatarresolver.GravatarHash("noimage@brand.example")); err != nil {
		t.Fatal(err)
	}
	// This lookup failed longer ago than the retry wait.
	if _, err := db.Write().ExecContext(ctx, `UPDATE avatar_provider_states SET checked_at = datetime('now', '-2 hours') WHERE email = 'retry@brand.example'`); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetOwnGmailAvatarCandidates(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	emails := map[string]bool{}
	for _, c := range got {
		emails[c.Email] = true
	}
	if len(got) != 3 || !emails["icon@brand.example"] || !emails["retry@brand.example"] || !emails["noimage@brand.example"] {
		t.Fatalf("candidates = %+v, want the stored icon, the mailbox without an image and the lookup due for a retry", got)
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
