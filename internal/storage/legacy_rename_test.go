package storage

import (
	"database/sql"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	renameDDLQuotes = regexp.MustCompile(`"`)
	renameDDLSpace  = regexp.MustCompile(`\s+`)
	renameDDLIfNot  = regexp.MustCompile(`(?i)\s+IF NOT EXISTS`)
)

// renameSchemaDump lists the schema as type|name|table|definition with quoting
// and spacing differences removed, so a migrated database can be compared with
// a fresh one.
func renameSchemaDump(t *testing.T, db *DB) string {
	t.Helper()
	rows, err := db.Read().Query(`SELECT type, name, tbl_name, COALESCE(sql,'') FROM sqlite_schema WHERE name NOT LIKE 'sqlite\_%' ESCAPE '\'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var dump []string
	for rows.Next() {
		var kind, name, table, definition string
		if err := rows.Scan(&kind, &name, &table, &definition); err != nil {
			t.Fatal(err)
		}
		definition = renameDDLSpace.ReplaceAllString(renameDDLQuotes.ReplaceAllString(renameDDLIfNot.ReplaceAllString(definition, ""), ""), " ")
		dump = append(dump, strings.Join([]string{kind, name, table, strings.TrimSpace(definition)}, "|"))
	}
	sort.Strings(dump)
	return strings.Join(dump, "\n")
}

// makeVersion121 puts db back to how a release from before the rename left it:
// totp_credentials with the old column default, schema version 121.
func makeVersion121(t *testing.T, db *DB) {
	t.Helper()
	legacy := strings.Replace(totpCredentialsSchema, "DEFAULT 'Raven'", "DEFAULT '"+legacyProductName+"'", 1)
	for _, statement := range []string{
		`ALTER TABLE totp_credentials RENAME TO totp_credentials_old`,
		`DROP INDEX idx_totp_credentials_active`,
		legacy,
		`INSERT INTO totp_credentials SELECT * FROM totp_credentials_old`,
		`DROP TABLE totp_credentials_old`,
		`CREATE UNIQUE INDEX idx_totp_credentials_active ON totp_credentials(user_id) WHERE revoked_at IS NULL`,
		`DELETE FROM schema_version`,
		`INSERT INTO schema_version(version) VALUES (121)`,
	} {
		if _, err := db.Write().Exec(statement); err != nil {
			t.Fatal(statement, err)
		}
	}
}

func TestMigrateV121ToV122RebuildsTotpCredentialsAndKeepsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "totp.db")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Write().Exec(`INSERT INTO users(id,username,username_normalized) VALUES ('alice','alice','alice'),('bob','bob','bob');
INSERT INTO totp_credentials(id,user_id,encrypted_seed,key_version,issuer,enabled,created_at,last_accepted_step)
 VALUES ('t-alice','alice',x'0102',1,'Raven',1,'2024-01-02 03:04:05',42);
INSERT INTO totp_credentials(id,user_id,encrypted_seed,key_version,issuer,enabled,created_at,revoked_at)
 VALUES ('t-bob','bob',x'0304',2,'Gofer',0,'2024-02-03 04:05:06','2024-03-04 05:06:07')`); err != nil {
		t.Fatal(err)
	}
	makeVersion121(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var version int
	var definition string
	if err := db.Read().QueryRow(`SELECT (SELECT MAX(version) FROM schema_version), (SELECT sql FROM sqlite_schema WHERE name='totp_credentials')`).Scan(&version, &definition); err != nil {
		t.Fatal(err)
	}
	if version != CurrentSchemaVersion || strings.Contains(definition, legacyProductName) || !strings.Contains(definition, "DEFAULT 'Raven'") {
		t.Fatalf("version %d, definition:\n%s", version, definition)
	}
	rows, err := db.Read().Query(`SELECT id, user_id, hex(encrypted_seed), key_version, issuer, enabled, created_at, last_accepted_step, revoked_at FROM totp_credentials ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id, user, seed, issuer, created string
		var keyVersion, enabled int
		var step sql.NullInt64
		var revoked sql.NullString
		if err := rows.Scan(&id, &user, &seed, &keyVersion, &issuer, &enabled, &created, &step, &revoked); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.Join([]string{id, user, seed, issuer, created}, "|"))
		if id == "t-alice" && (keyVersion != 1 || enabled != 1 || step.Int64 != 42 || revoked.Valid) {
			t.Fatalf("alice's row changed: %d %d %v %v", keyVersion, enabled, step, revoked)
		}
		if id == "t-bob" && (keyVersion != 2 || enabled != 0 || step.Valid || !revoked.Valid) {
			t.Fatalf("bob's row changed: %d %d %v %v", keyVersion, enabled, step, revoked)
		}
	}
	if len(got) != 2 || !strings.HasPrefix(got[0], "t-alice|alice|0102|Raven|") || !strings.HasPrefix(got[1], "t-bob|bob|0304|Raven|") {
		t.Fatalf("rows = %v", got)
	}
	// The one-active-credential rule survived the rebuild.
	if _, err := db.Write().Exec(`INSERT INTO totp_credentials(id,user_id,encrypted_seed,key_version) VALUES ('second','alice',x'05',1)`); err == nil {
		t.Fatal("a second active credential was accepted")
	}
	// Migrating twice is harmless.
	for range 2 {
		if err := migrateV121ToV122(db.Write()); err != nil {
			t.Fatal("replay", err)
		}
	}
}

func TestVersion122SchemaMatchesAFreshInstall(t *testing.T) {
	dir := t.TempDir()
	migratedPath := filepath.Join(dir, "migrated.db")
	db, err := New(migratedPath)
	if err != nil {
		t.Fatal(err)
	}
	makeVersion121(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	migrated, err := New(migratedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	fresh, err := New(filepath.Join(dir, "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if got, want := renameSchemaDump(t, migrated), renameSchemaDump(t, fresh); got != want {
		t.Fatalf("migrated schema differs from a fresh install\nmigrated:\n%s\nfresh:\n%s", got, want)
	}
	if strings.Contains(strings.ToLower(renameSchemaDump(t, fresh)), strings.ToLower(legacyProductName)) {
		t.Fatal("a fresh schema still names the old product")
	}
}

func TestMigrateV121ToV122LeavesANewDefaultAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "already-new.db")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Write().Exec(`DELETE FROM schema_version; INSERT INTO schema_version(version) VALUES (121)`); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := db.Read().QueryRow(`SELECT sql FROM sqlite_schema WHERE name='totp_credentials'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := migrateV121ToV122(db.Write()); err != nil {
		t.Fatal(err)
	}
	var after string
	var version int
	if err := db.Read().QueryRow(`SELECT (SELECT sql FROM sqlite_schema WHERE name='totp_credentials'), (SELECT MAX(version) FROM schema_version)`).Scan(&after, &version); err != nil {
		t.Fatal(err)
	}
	if before != after || version != 122 {
		t.Fatalf("table changed or version = %d", version)
	}
}
