package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewRestrictsDatabaseFilesToOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "gofer.db")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Write().Exec(`CREATE TABLE IF NOT EXISTS perm_probe (x)`); err != nil { // forces -wal/-shm to exist
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("data dir mode = %v, %v; want 0700", info.Mode().Perm(), err)
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(p)
		if os.IsNotExist(err) {
			continue // journal mode decides whether these exist
		}
		if err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s mode = %v, %v; want no group/other access", filepath.Base(p), info.Mode().Perm(), err)
		}
	}

	// An existing, loosely permissioned database is tightened on the next start.
	db.Close()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	db2, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("existing db mode = %v, want 0600", info.Mode().Perm())
	}
}
