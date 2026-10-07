package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoredMailIsOwnerOnly(t *testing.T) {
	root := t.TempDir()
	s := NewBlobStore(filepath.Join(root, "accounts"))
	ctx := t.Context()
	var paths []string
	for _, store := range []func() (string, error){
		func() (string, error) { return s.StoreRaw(ctx, "acc", 1, []byte("raw")) },
		func() (string, error) { return s.StoreBodyText(ctx, "acc", 1, []byte("t")) },
		func() (string, error) { return s.StoreBodyHTML(ctx, "acc", 1, []byte("<p>h</p>")) },
		func() (string, error) { return s.StoreRemoteBodyHTML("acc", 1, []byte("<p>h</p>")) },
		func() (string, error) { return s.StoreRemoteAsset("acc", 1, "https://x.example/a.png", []byte("png")) },
	} {
		p, err := store()
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	for _, p := range paths {
		if info, err := os.Stat(p); err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s mode = %v, %v; want owner only", filepath.Base(p), info.Mode().Perm(), err)
		}
	}
	_ = filepath.WalkDir(filepath.Join(root, "accounts"), func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			if info, _ := d.Info(); info.Mode().Perm()&0o077 != 0 && strings.HasPrefix(p, root) {
				t.Errorf("dir %s mode = %v, want owner only", p, info.Mode().Perm())
			}
		}
		return nil
	})
}
