package config

import (
	"context"
	"database/sql"
	"errors"

	"github.com/fahadjibransheikh/raven/internal/storage"
)

// ValidateUserStorageMigrationPasswords authenticates every retained password,
// including disabled/deleting mailboxes and inactive CardDAV/CalDAV settings.
// It neither filters by runtime eligibility nor rewrites ciphertext or calls a
// provider. The offline coordinator must hold the source runtime lock.
func ValidateUserStorageMigrationPasswords(ctx context.Context, source *storage.DB, key []byte) error {
	if ctx == nil || source == nil || len(key) != 32 {
		return errors.New("password migration verification requires source, context and application key")
	}
	codec, err := NewAccountStore(nil, key)
	if err != nil {
		return err
	}
	tx, err := source.Read().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	passwords, err := listStoredPasswords(ctx, tx)
	if err != nil {
		return err
	}
	for _, p := range passwords {
		if !codec.authenticatesPassword(p.userID, p.accountID, p.kind, p.payload) {
			return errors.New("application key cannot authenticate a retained mailbox or DAV password")
		}
	}
	return nil
}
