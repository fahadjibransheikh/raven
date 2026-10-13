package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/runtimeguard"
)

func writeFiles(t *testing.T, dir string, names map[string]string) {
	t.Helper()
	for name, content := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func listDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[entry.Name()] = string(data)
	}
	return out
}

func TestAdoptLegacyDatabaseRenamesAllFiles(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"gofer.db": "db", "gofer.db-wal": "wal", "gofer.db-shm": "shm", "gofer.db.lock": "", "secret.key": "k"})
	if err := adoptLegacyDatabase(filepath.Join(dir, "raven.db")); err != nil {
		t.Fatal(err)
	}
	got := listDir(t, dir)
	want := map[string]string{"raven.db": "db", "raven.db-wal": "wal", "raven.db-shm": "shm", "raven.db.lock": "", "secret.key": "k"}
	if len(got) != len(want) {
		t.Fatalf("files = %v; want %v", got, want)
	}
	for name, content := range want {
		if got[name] != content {
			t.Fatalf("%s = %q; want %q (all: %v)", name, got[name], content, got)
		}
	}
}

func TestAdoptLegacyDatabaseWithoutOptionalCompanions(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"gofer.db": "db"})
	if err := adoptLegacyDatabase(filepath.Join(dir, "raven.db")); err != nil {
		t.Fatal(err)
	}
	if got := listDir(t, dir); len(got) != 2 || got["raven.db"] != "db" {
		// Acquire creates the lock file, and it travels with the database.
		t.Fatalf("files = %v", got)
	}
}

func TestAdoptLegacyDatabaseLeavesBothAlone(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"gofer.db": "old", "gofer.db-wal": "oldwal", "raven.db": "new"}
	writeFiles(t, dir, files)
	if err := adoptLegacyDatabase(filepath.Join(dir, "raven.db")); err != nil {
		t.Fatal(err)
	}
	got := listDir(t, dir)
	if len(got) != len(files) {
		t.Fatalf("files = %v; want %v", got, files)
	}
	for name, content := range files {
		if got[name] != content {
			t.Fatalf("%s = %q; want %q", name, got[name], content)
		}
	}
}

func TestAdoptLegacyDatabaseNothingToDo(t *testing.T) {
	dir := t.TempDir()
	if err := adoptLegacyDatabase(filepath.Join(dir, "raven.db")); err != nil {
		t.Fatal(err)
	}
	if got := listDir(t, dir); len(got) != 0 {
		t.Fatalf("created files: %v", got)
	}
}

func TestAdoptLegacyDatabaseFinishesAnInterruptedMove(t *testing.T) {
	dir := t.TempDir()
	// The -wal was already renamed; the database file was not.
	writeFiles(t, dir, map[string]string{"gofer.db": "db", "raven.db-wal": "wal"})
	if err := adoptLegacyDatabase(filepath.Join(dir, "raven.db")); err != nil {
		t.Fatal(err)
	}
	got := listDir(t, dir)
	if got["raven.db"] != "db" || got["raven.db-wal"] != "wal" {
		t.Fatalf("files = %v", got)
	}
}

func TestAdoptLegacyDatabaseRefusesToOverwriteACompanion(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"gofer.db": "db", "gofer.db-wal": "old", "raven.db-wal": "other"})
	if err := adoptLegacyDatabase(filepath.Join(dir, "raven.db")); err == nil {
		t.Fatal("expected a conflict error")
	}
	if got := listDir(t, dir); got["gofer.db"] != "db" || got["raven.db-wal"] != "other" {
		t.Fatalf("files changed on conflict: %v", got)
	}
}

func TestAdoptLegacyDatabaseRefusesWhileInUse(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"gofer.db": "db"})
	lock, err := runtimeguard.Acquire(filepath.Join(dir, "gofer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := adoptLegacyDatabase(filepath.Join(dir, "raven.db")); err == nil {
		t.Fatal("expected an in-use error")
	}
	if _, err := os.Stat(filepath.Join(dir, "gofer.db")); err != nil {
		t.Fatalf("database moved while in use: %v", err)
	}
}

func TestConfiguredDatabasePathHonoursExplicitPath(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"gofer.db": "db"})
	explicit := filepath.Join(dir, "mine.db")
	t.Setenv("RAVEN_DB_PATH", explicit)
	if got := ConfiguredDatabasePath(); got != explicit {
		t.Fatalf("path = %q; want %q", got, explicit)
	}
	if got := listDir(t, dir); len(got) != 1 || got["gofer.db"] != "db" {
		t.Fatalf("explicit path touched files: %v", got)
	}
}

func TestConfiguredDatabasePathAdoptsLegacyDefault(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.Mkdir("data", 0o700); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, filepath.Join(root, "data"), map[string]string{"gofer.db": "db"})
	t.Setenv("RAVEN_DB_PATH", "")
	t.Setenv("GOFER_DB_PATH", "")
	if got := ConfiguredDatabasePath(); got != "data/raven.db" {
		t.Fatalf("path = %q", got)
	}
	if _, err := os.Stat("data/raven.db"); err != nil {
		t.Fatalf("raven.db missing: %v", err)
	}
}
