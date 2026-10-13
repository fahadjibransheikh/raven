package server

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/mailauth"
	"github.com/fahadjibransheikh/raven/internal/storage"
)

// legacyify returns the layout database at target to how a release from before
// the rename left it: gofer_-prefixed tables, indexes and triggers, the old
// totp_credentials default, layout version 1 and schema version 121.
func legacyify(t *testing.T, target string) {
	t.Helper()
	db, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := db.Exec(statement, args...); err != nil {
			t.Fatal(statement, err)
		}
	}
	// raven_calendar_carryover existed before the rename and keeps its name.
	toLegacy := func(statement string) string {
		statement = strings.ReplaceAll(statement, "raven_calendar_carryover", "\x00")
		statement = strings.ReplaceAll(statement, "raven_", "gofer_")
		return strings.ReplaceAll(statement, "\x00", "raven_calendar_carryover")
	}
	type object struct{ kind, name, sql string }
	var objects []object
	rows, err := db.Query(`SELECT type, name, sql FROM sqlite_schema WHERE type IN ('trigger','index') AND sql IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var o object
		if err := rows.Scan(&o.kind, &o.name, &o.sql); err != nil {
			t.Fatal(err)
		}
		objects = append(objects, o)
	}
	rows.Close()
	for _, o := range objects {
		exec(`DROP ` + strings.ToUpper(o.kind) + ` "` + o.name + `"`)
	}
	var totp string
	if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name='totp_credentials'`).Scan(&totp); err != nil {
		t.Fatal(err)
	}
	exec(`ALTER TABLE totp_credentials RENAME TO totp_credentials_old`)
	exec(strings.Replace(totp, "DEFAULT 'Raven'", "DEFAULT 'Gofer'", 1))
	exec(`INSERT INTO totp_credentials SELECT * FROM totp_credentials_old`)
	exec(`DROP TABLE totp_credentials_old`)
	var tables []string
	rows, err = db.Query(`SELECT name FROM sqlite_schema WHERE type='table' AND name LIKE 'raven\_%' ESCAPE '\' AND name <> 'raven_calendar_carryover'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	for _, name := range tables {
		exec(`ALTER TABLE "` + name + `" RENAME TO "` + toLegacy(name) + `"`)
	}
	for _, o := range objects {
		exec(toLegacy(o.sql))
	}
	exec(`PRAGMA user_version=1`)
	exec(`DELETE FROM schema_version WHERE version > 121`)
	exec(`INSERT OR REPLACE INTO schema_version(version) VALUES (121)`)
}

var (
	ddlQuotes = regexp.MustCompile(`["]`)
	ddlSpace  = regexp.MustCompile(`\s+`)
	ddlIfNot  = regexp.MustCompile(`(?i)\s+IF NOT EXISTS`)
)

// schemaDump lists every schema object as type|name|table|definition, with the
// quoting and spacing differences between a renamed and a newly created object
// removed, so a migrated database can be compared with a fresh one.
func schemaDump(t *testing.T, target string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT type, name, tbl_name, COALESCE(sql,'') FROM sqlite_schema WHERE name NOT LIKE 'sqlite\_%' ESCAPE '\'`)
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
		definition = ddlSpace.ReplaceAllString(ddlQuotes.ReplaceAllString(ddlIfNot.ReplaceAllString(definition, ""), ""), " ")
		dump = append(dump, strings.Join([]string{kind, name, table, strings.TrimSpace(definition)}, "|"))
	}
	sort.Strings(dump)
	return dump
}

func scalar(t *testing.T, target, query string) string {
	t.Helper()
	db, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow(query).Scan(&value); err != nil {
		t.Fatal(query, err)
	}
	return value
}

func TestManagedStorageUpgradesLayoutObjectsLeftBeforeTheRename(t *testing.T) {
	for _, adoptedWithoutVersion := range []bool{false, true} {
		name := "recorded layout version"
		if adoptedWithoutVersion {
			name = "layout published before versioning"
		}
		t.Run(name, func(t *testing.T) {
			build := func() (string, []string) {
				options, _, key := migrationCommandFixture(t)
				if _, err := storage.MigrateUserStorage(t.Context(), options); err != nil {
					t.Fatal(err)
				}
				t.Setenv("RAVEN_SECRET_KEY", hex.EncodeToString(key))
				path := options.DestinationPath
				targets := []string{path}
				for _, owner := range []string{"alice", "bob"} {
					hash := sha256.Sum256([]byte(owner))
					targets = append(targets, filepath.Join(path+".users", hex.EncodeToString(hash[:])+".db"))
				}
				return path, targets
			}
			freshPath, fresh := build()
			legacyPath, legacy := build()
			for _, target := range legacy {
				legacyify(t, target)
			}
			if adoptedWithoutVersion {
				for _, target := range legacy {
					db, err := sql.Open("sqlite", target)
					if err != nil {
						t.Fatal(err)
					}
					_, err = db.Exec(`PRAGMA user_version=0`)
					db.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if scalar(t, legacy[0], `SELECT count(*) FROM sqlite_schema WHERE name LIKE 'gofer\_%' ESCAPE '\'`) == "0" {
				t.Fatal("fixture was not turned into a pre-rename layout")
			}
			rowCounts := func(target string) string {
				return scalar(t, target, `SELECT (SELECT count(*) FROM `+firstTable(t, target, "raven_account_directory", "gofer_account_directory")+`)||'/'||
					(SELECT count(*) FROM `+firstTable(t, target, "raven_mailbox_credentials", "gofer_mailbox_credentials")+`)||'/'||
					(SELECT count(*) FROM users)||'/'||(SELECT count(*) FROM accounts)`)
			}
			before := rowCounts(legacy[0])

			for _, path := range []string{freshPath, legacyPath} {
				s, err := openManagedStorage(t.Context(), path, 1)
				if err != nil {
					t.Fatal(path, err)
				}
				// The managed runtime also starts the mailbox credential store, which
				// recreates that table's triggers.
				if _, err := mailauth.NewUserCredentials(t.Context(), nil, s.routing, s.key); err != nil {
					t.Fatal(path, err)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
			for i, target := range legacy {
				if got := scalar(t, target, `SELECT count(*) FROM sqlite_schema WHERE lower(name) LIKE '%gofer%' OR lower(COALESCE(sql,'')) LIKE '%gofer%'`); got != "0" {
					t.Fatalf("%s still has %s objects that mention the old name", filepath.Base(target), got)
				}
				if got := scalar(t, target, `PRAGMA user_version`); got != "2" {
					t.Fatalf("%s layout version = %s, want 2", filepath.Base(target), got)
				}
				if got := scalar(t, target, `PRAGMA integrity_check`); got != "ok" {
					t.Fatalf("%s integrity_check = %s", filepath.Base(target), got)
				}
				if got := scalar(t, target, `SELECT count(*) FROM pragma_foreign_key_check`); got != "0" {
					t.Fatalf("%s has %s foreign key violations", filepath.Base(target), got)
				}
				if got, want := schemaDump(t, target), schemaDump(t, fresh[i]); strings.Join(got, "\n") != strings.Join(want, "\n") {
					t.Fatalf("%s: migrated schema differs from a fresh layout\nmigrated only: %v\nfresh only: %v", filepath.Base(target), diff(got, want), diff(want, got))
				}
			}
			if after := rowCounts(legacy[0]); after != before {
				t.Fatalf("central row counts changed: %s -> %s", before, after)
			}
		})
	}
}

func firstTable(t *testing.T, target string, names ...string) string {
	t.Helper()
	for _, name := range names {
		if scalar(t, target, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='`+name+`'`) != "0" {
			return name
		}
	}
	t.Fatalf("none of %v exists in %s", names, target)
	return ""
}

func diff(a, b []string) []string {
	in := map[string]bool{}
	for _, v := range b {
		in[v] = true
	}
	var out []string
	for _, v := range a {
		if !in[v] {
			out = append(out, v)
		}
	}
	return out
}
