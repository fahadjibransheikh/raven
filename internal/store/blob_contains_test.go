package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBlobStoreContains(t *testing.T) {
	root := t.TempDir()
	s := NewBlobStore(filepath.Join(root, "blobs"))
	in, err := s.StoreRaw(t.Context(), "acct", 1, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.eml")
	_ = os.WriteFile(outside, []byte("x"), 0o600)
	link := filepath.Join(filepath.Dir(in), "link.eml")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{in: true, outside: false, link: false, filepath.Join(root, "blobs", "..", "outside.eml"): false, filepath.Join(root, "missing"): false} {
		if got := s.Contains(path); got != want {
			t.Errorf("Contains(%q) = %v, want %v", path, got, want)
		}
	}
}
