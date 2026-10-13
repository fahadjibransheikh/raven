package config

import (
	"crypto/rand"
	"io"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

// legacyEncryptPassword writes the format used before passwords were bound to
// their account.
func legacyEncryptPassword(s *AccountStore, plaintext string) ([]byte, error) {
	nonce := make([]byte, s.aes.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return s.aes.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func TestStoredPasswordsOnlyWorkForTheirOwnAccount(t *testing.T) {
	ctx := t.Context()
	db, store := newAccountStoreTestStore(t)
	seedAccountStoreTestUser(t, ctx, db)
	victimReq := secureAccountStoreTestRequest("rosa@example.com")
	victimReq.Password = "rosa-mail-secret"
	victimReq.SmtpUsername, victimReq.SmtpPassword = "rosa-smtp", "rosa-smtp-secret"
	victim, err := store.CreateAccount(ctx, "default", victimReq)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAccount(ctx, "default", secureAccountStoreTestRequest("leo@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if password, err := store.DecryptPassword(ctx, victim.ID); err != nil || password != "rosa-mail-secret" {
		t.Fatalf("DecryptPassword() = %q, %v", password, err)
	}
	if password, err := store.DecryptSmtpPassword(ctx, victim.ID); err != nil || password != "rosa-smtp-secret" {
		t.Fatalf("DecryptSmtpPassword() = %q, %v", password, err)
	}

	// A password copied into another account, or into another service of the
	// same account, does not decrypt there.
	if _, err := db.Write().ExecContext(ctx, `UPDATE accounts SET encrypted_password = (SELECT encrypted_password FROM accounts WHERE id = ?) WHERE id = ?`, victim.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if password, err := store.DecryptPassword(ctx, other.ID); err == nil {
		t.Fatalf("copied password decrypted for another account: %q", password)
	}
	if _, err := db.Write().ExecContext(ctx, `UPDATE accounts SET encrypted_smtp_password = encrypted_password WHERE id = ?`, victim.ID); err != nil {
		t.Fatal(err)
	}
	if password, err := store.DecryptSmtpPassword(ctx, victim.ID); err == nil {
		t.Fatalf("mail password decrypted as the sending password: %q", password)
	}

	// Passwords saved for contacts and calendars are bound the same way.
	if err := store.SaveContactSyncConfig(ctx, "default", victim.ID, models.ContactSyncConfig{Enabled: true, BaseURL: "https://dav.example.com/"}, "rosa-contacts-secret"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCalDAVConfig(ctx, "default", victim.ID, "https://dav.example.com/", "rosa", "rosa-calendar-secret", false); err != nil {
		t.Fatal(err)
	}
	if password, err := store.DecryptContactSyncPassword(ctx, "default", victim.ID); err != nil || password != "rosa-contacts-secret" {
		t.Fatalf("DecryptContactSyncPassword() = %q, %v", password, err)
	}
	if password, err := store.CalDAVPassword(ctx, "default", victim.ID); err != nil || password != "rosa-calendar-secret" {
		t.Fatalf("CalDAVPassword() = %q, %v", password, err)
	}
	if _, err := db.Write().ExecContext(ctx, `UPDATE account_caldav_configs SET encrypted_password = (SELECT encrypted_password FROM account_contact_sync_configs) WHERE account_id = ?`, victim.ID); err != nil {
		t.Fatal(err)
	}
	if password, err := store.CalDAVPassword(ctx, "default", victim.ID); err == nil {
		t.Fatalf("contacts password decrypted as the calendar password: %q", password)
	}
}

func TestOldStoredPasswordsAreUpgradedAndThenRefused(t *testing.T) {
	ctx := t.Context()
	db, store := newAccountStoreTestStore(t)
	seedAccountStoreTestUser(t, ctx, db)
	account, err := store.CreateAccount(ctx, "default", secureAccountStoreTestRequest("rosa@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	old, err := legacyEncryptPassword(store, "rosa-mail-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().ExecContext(ctx, `UPDATE accounts SET encrypted_password = ? WHERE id = ?`, old, account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DecryptPassword(ctx, account.ID); err == nil {
		t.Fatal("old format accepted before the upgrade")
	}
	if err := ValidateUserStorageMigrationPasswords(ctx, db, []byte("0123456789abcdef0123456789abcdef")); err != nil {
		t.Fatalf("migration check refused an old password: %v", err)
	}

	if err := store.SecurePasswords(ctx); err != nil {
		t.Fatal(err)
	}
	if password, err := store.DecryptPassword(ctx, account.ID); err != nil || password != "rosa-mail-secret" {
		t.Fatalf("DecryptPassword() after upgrade = %q, %v", password, err)
	}
	if err := ValidateUserStorageMigrationPasswords(ctx, db, []byte("0123456789abcdef0123456789abcdef")); err != nil {
		t.Fatalf("migration check refused an upgraded password: %v", err)
	}
	// Running it again changes nothing.
	var before, after []byte
	_ = db.Read().QueryRowContext(ctx, `SELECT encrypted_password FROM accounts WHERE id = ?`, account.ID).Scan(&before)
	if err := store.SecurePasswords(ctx); err != nil {
		t.Fatal(err)
	}
	_ = db.Read().QueryRowContext(ctx, `SELECT encrypted_password FROM accounts WHERE id = ?`, account.ID).Scan(&after)
	if string(before) != string(after) {
		t.Fatal("upgrade rewrote an already upgraded password")
	}

	// An old-format password written after the upgrade is not accepted, and
	// later starts don't upgrade it for its new row.
	if _, err := db.Write().ExecContext(ctx, `UPDATE accounts SET encrypted_password = ? WHERE id = ?`, old, account.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SecurePasswords(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DecryptPassword(ctx, account.ID); err == nil {
		t.Fatal("old format accepted after the upgrade")
	}
}
