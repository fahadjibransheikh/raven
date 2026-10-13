package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Schema version 122 and layout step 2 finish the rename from the project Raven
// began as. This file is the only place that still knows the old names.
//
//   - migrateV121ToV122 rebuilds totp_credentials, whose column default carried
//     the old product name, and moves existing rows to the new one.
//   - renameLegacyLayoutObjects renames the old-prefixed tables of a per-user
//     layout (central and user databases), which keep their own version in
//     PRAGMA user_version, and drops their old-named indexes and triggers so the
//     layout steps recreate them under the new names.
//
// Shared (personal and open mode) databases have no layout objects, so for them
// version 122 only touches totp_credentials.

const (
	legacyObjectPrefix  = "gofer_"
	currentObjectPrefix = "raven_"
)

// TableNameList returns names, and the pre-rename twin of each, as a quoted
// SQL list for IN (...). Code that can meet a layout database before it is
// upgraded has to recognise both spellings.
func TableNameList(names ...string) string {
	quoted := make([]string, 0, 2*len(names))
	for _, name := range names {
		quoted = append(quoted, "'"+name+"'", "'"+legacyTableName(name)+"'")
	}
	return strings.Join(quoted, ",")
}

// legacyTableName returns name with the pre-rename prefix in place of the
// current one.
func legacyTableName(name string) string {
	return legacyObjectPrefix + strings.TrimPrefix(name, currentObjectPrefix)
}

// firstExistingTable returns the first of names that is a table in the main
// schema, or "" if none is.
func firstExistingTable(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, names ...string) (string, error) {
	for _, name := range names {
		var n int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM main.sqlite_schema WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
			return "", err
		}
		if n > 0 {
			return name, nil
		}
	}
	return "", nil
}

// renameLegacyLayoutObjects renames every table with the old prefix to the new
// one, first dropping the old-prefixed triggers and indexes that sit on them.
// SQLite rewrites foreign keys that point at a renamed table. A database with
// nothing to rename is left alone; a new-named table that already exists next
// to its old-named twin is an error rather than a merge.
func renameLegacyLayoutObjects(ctx context.Context, tx *sql.Tx) error {
	names := func(kind string, patterns ...string) ([]string, error) {
		query := `SELECT name FROM sqlite_schema WHERE type=? AND (`
		args := []any{kind}
		for i, pattern := range patterns {
			if i > 0 {
				query += ` OR `
			}
			query += `name LIKE ? ESCAPE '\'`
			args = append(args, pattern)
		}
		rows, err := tx.QueryContext(ctx, query+`) ORDER BY name`, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return nil, err
			}
			out = append(out, name)
		}
		return out, rows.Err()
	}
	escaped := strings.ReplaceAll(legacyObjectPrefix, "_", `\_`)
	tables, err := names("table", escaped+"%")
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return nil
	}
	for _, kind := range []string{"trigger", "index"} {
		objects, err := names(kind, escaped+"%", "idx\\_"+escaped+"%")
		if err != nil {
			return err
		}
		for _, name := range objects {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DROP %s IF EXISTS %s`, strings.ToUpper(kind), quoteStoreIdentifier(name))); err != nil {
				return err
			}
		}
	}
	for _, old := range tables {
		next := currentObjectPrefix + strings.TrimPrefix(old, legacyObjectPrefix)
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`, quoteStoreIdentifier(old), quoteStoreIdentifier(next))); err != nil {
			return fmt.Errorf("rename %s to %s: %w", old, next, err)
		}
	}
	return nil
}

// totpCredentialsSchema is totp_credentials as schema.sql defines it.
const totpCredentialsSchema = `CREATE TABLE totp_credentials (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    encrypted_seed BLOB NOT NULL CHECK (length(encrypted_seed) > 0),
    key_version INTEGER NOT NULL CHECK (key_version > 0),
    algorithm TEXT NOT NULL DEFAULT 'SHA1' CHECK (algorithm IN ('SHA1', 'SHA256', 'SHA512')),
    digits INTEGER NOT NULL DEFAULT 6 CHECK (digits IN (6, 8)),
    period INTEGER NOT NULL DEFAULT 30 CHECK (period > 0),
    issuer TEXT NOT NULL DEFAULT 'Raven' CHECK (issuer <> ''),
    last_accepted_step INTEGER CHECK (last_accepted_step IS NULL OR last_accepted_step >= 0),
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME,
    revoked_at DATETIME,
    CHECK (revoked_at IS NULL OR enabled = 0)
)`

// migrateV121ToV122 gives totp_credentials the new column default and renames
// the product name stored in existing rows. SQLite cannot change a default in
// place, so the table is rebuilt. It is skipped when the table already has the
// new default (new installs, and databases that passed through the older
// migrations after this default changed). In a user database the rebuild drops
// the table's boundary triggers; the layout upgrade restores them afterwards.
func migrateV121ToV122(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&version); err != nil {
		return err
	}
	if version >= 122 {
		return nil
	}
	var definition sql.NullString
	if err := tx.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name='totp_credentials'`).Scan(&definition); err != nil && err != sql.ErrNoRows {
		return err
	}
	if strings.Contains(definition.String, "DEFAULT '"+legacyProductName+"'") {
		for _, statement := range []string{
			`ALTER TABLE totp_credentials RENAME TO totp_credentials_v121`,
			`DROP INDEX IF EXISTS idx_totp_credentials_active`,
			totpCredentialsSchema,
			`INSERT INTO totp_credentials (id, user_id, encrypted_seed, key_version, algorithm, digits, period, issuer,
				last_accepted_step, enabled, created_at, last_used_at, revoked_at)
			 SELECT id, user_id, encrypted_seed, key_version, algorithm, digits, period,
				CASE issuer WHEN '` + legacyProductName + `' THEN 'Raven' ELSE issuer END,
				last_accepted_step, enabled, created_at, last_used_at, revoked_at
			 FROM totp_credentials_v121`,
			`DROP TABLE totp_credentials_v121`,
			`CREATE UNIQUE INDEX idx_totp_credentials_active ON totp_credentials(user_id) WHERE revoked_at IS NULL`,
		} {
			if _, err := tx.Exec(statement); err != nil {
				return fmt.Errorf("rebuild totp_credentials: %w", err)
			}
		}
	}
	if err := markSchemaVersion(tx, 122); err != nil {
		return err
	}
	return tx.Commit()
}

const legacyProductName = "Gofer"
