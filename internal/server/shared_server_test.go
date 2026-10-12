package server

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestReadOrCreateSecretKeyNeverReplacesAnExistingKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data", "secret.key")

	key, err := readOrCreateSecretKey(path)
	if err != nil || len(key) != 32 {
		t.Fatalf("create: key len %d, err %v", len(key), err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	again, err := readOrCreateSecretKey(path)
	if err != nil || !bytes.Equal(again, key) {
		t.Fatalf("second read changed the key or failed: %v", err)
	}

	// Wrong size (truncated write, bad restore): refuse, and leave the file alone.
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readOrCreateSecretKey(path); err == nil {
		t.Fatal("malformed key file was accepted or silently replaced")
	}
	if got, _ := os.ReadFile(path); string(got) != "short" {
		t.Fatalf("malformed key file was overwritten: %q", got)
	}

	// Unreadable (not merely missing): refuse instead of generating a new key.
	if os.Geteuid() != 0 {
		if err := os.WriteFile(path, key, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(path, 0o600) })
		if _, err := readOrCreateSecretKey(path); err == nil {
			t.Fatal("unreadable key file was replaced instead of reported")
		}
	}
}
