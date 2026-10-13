package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestComposeAttachmentsAreScopedByUser(t *testing.T) {
	store := NewBlobStore(t.TempDir())
	id, path, err := store.StoreComposeAttachment(t.Context(), "owner", "private.txt", bytes.NewBufferString("private"))
	if err != nil {
		t.Fatalf("StoreComposeAttachment() error = %v", err)
	}
	if _, err := store.ComposeAttachmentPath("owner", id); err != nil {
		t.Fatalf("owner ComposeAttachmentPath() error = %v", err)
	}
	if _, err := store.ComposeAttachmentPath("attacker", id); !os.IsNotExist(err) {
		t.Fatalf("foreign ComposeAttachmentPath() error = %v, want not exist", err)
	}
	if err := store.DeleteComposeAttachment("attacker", id); err != nil {
		t.Fatalf("foreign DeleteComposeAttachment() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("foreign delete removed owner file: %v", err)
	}
	if err := store.DeleteComposeAttachment("owner", id); err != nil {
		t.Fatalf("owner DeleteComposeAttachment() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("owner file still exists: %v", err)
	}
}

func TestCleanupComposeAttachmentsVisitsUserDirectories(t *testing.T) {
	store := NewBlobStore(t.TempDir())
	_, path, err := store.StoreComposeAttachment(t.Context(), "owner", "old.txt", bytes.NewBufferString("old"))
	if err != nil {
		t.Fatalf("StoreComposeAttachment() error = %v", err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}
	removed, err := store.CleanupComposeAttachments(time.Hour, nil)
	if err != nil {
		t.Fatalf("CleanupComposeAttachments() error = %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("old compose attachment still exists: %v", err)
	}
}

func TestDeleteAccountRejectsPathsOutsideAnAccountDirectory(t *testing.T) {
	base := t.TempDir()
	store := NewBlobStore(base)
	marker := filepath.Join(base, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, accountID := range []string{"", ".", "..", "../outside", "nested/account", `nested\account`} {
		if err := store.DeleteAccount(accountID); err == nil {
			t.Fatalf("DeleteAccount(%q) error = nil", accountID)
		}
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("account path rejection removed base marker: %v", err)
	}
}

func TestAssetExtensionComesFromContentNotURL(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	html := []byte("<html><script>alert(1)</script></html>")
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)
	cases := []struct {
		name, url string
		data      []byte
		want      string
	}{
		{"png bytes, misleading url", "https://evil.example/x?.svg", png, ".png"},
		{"html bytes, image url", "https://evil.example/x.png", html, ""},
		{"html bytes, svg in query", "https://evil.example/x?.svg", html, ""},
		{"svg bytes, svg path", "https://cdn.example/logo.svg?v=2", svg, ".svg"},
		{"svg bytes, no svg path", "https://cdn.example/logo", svg, ""},
	}
	for _, tc := range cases {
		if got := assetExtension(tc.url, tc.data); got != tc.want {
			t.Errorf("%s: assetExtension(%q) = %q, want %q", tc.name, tc.url, got, tc.want)
		}
	}
}

func TestWriteRemoteAssetReplacesWholeAndLeavesNoTemporaryFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "remote_assets")
	png := append([]byte("\x89PNG\r\n\x1a\n"), 1, 2, 3)
	const remote = "https://img.example/a"
	path, err := WriteRemoteAsset(dir, remote, png)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, RemoteAssetName(remote)+".png"); path != want {
		t.Fatalf("path = %s, want %s", path, want)
	}
	if _, err := WriteRemoteAsset(dir, remote, append(png, 4)); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, append(png, 4)) {
		t.Fatalf("file = %v", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("directory holds %d entries, want 1", len(entries))
	}
}
