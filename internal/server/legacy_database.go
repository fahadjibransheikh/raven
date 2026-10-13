package server

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/fahadjibransheikh/raven/internal/runtimeguard"
)

const (
	defaultDatabasePath = "data/raven.db"
	legacyDatabaseName  = "gofer.db"
)

// Companion files SQLite and Raven keep next to a database. The database file
// itself is renamed last: if the process dies part-way, the next start still
// sees "no raven.db, gofer.db present" and finishes the move, and a database is
// never left without its write-ahead log.
var databaseCompanions = []string{"-wal", "-shm", ".lock"}

// adoptLegacyDatabase renames <dir>/gofer.db and its -wal, -shm and .lock files
// to <dir>/raven.* when path is the first and only the second does not exist.
// Nothing is copied or deleted. If raven.db already exists it is used as is and
// nothing is touched. A running server still holding the old database makes it
// fail rather than split the data between two names.
func adoptLegacyDatabase(path string) error {
	dir := filepath.Dir(path)
	legacy := filepath.Join(dir, legacyDatabaseName)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Stat(legacy); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	lock, err := runtimeguard.Acquire(legacy)
	if err != nil {
		return fmt.Errorf("%s is in use: %w", legacy, err)
	}
	// The lock file is renamed with the database, so release it first.
	if err := lock.Close(); err != nil {
		return err
	}
	for _, suffix := range append(append([]string{}, databaseCompanions...), "") {
		from, to := legacy+suffix, path+suffix
		if _, err := os.Lstat(from); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if _, err := os.Lstat(to); err == nil {
			return fmt.Errorf("both %s and %s exist; move one aside", from, to)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
		log.Printf("storage: renamed %s to %s", from, to)
	}
	return nil
}
