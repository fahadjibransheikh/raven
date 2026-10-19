package notifications

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/storage"
)

// A body cached before a sanitizer change is cleaned again when served, as the
// shared path does: tracking parameters and pixels go, ordinary links stay.
func TestUserCachedBodyIsSanitizedWhenServed(t *testing.T) {
	f := newUserStorageFixtureMode(t, true)
	server := newRoutedIMAPServer(t)
	f.useIMAPServer(t, server)
	if err := f.imap.Sync(t.Context(), "alice", f.accounts["alice"].ID); err != nil {
		t.Fatal(err)
	}
	if r := f.request("alice", "POST", "/api/messages/1/prefetch-body", ""); r.Code != 204 {
		t.Fatal("prefetch", r.Code, r.Body.String())
	}
	old := filepath.Join(t.TempDir(), "old-body.html")
	if err := os.WriteFile(old, []byte(`<p>stalebodytoken <a href="https://shop.example/item?id=7&amp;utm_source=news">item</a><img src="https://t.example/p.gif" width="1" height="1"></p>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.routing.WithUser(t.Context(), "alice", func(db *storage.DB) error {
		_, err := db.Write().Exec(`UPDATE messages SET body_html_path = ? WHERE id = 1`, old)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	r := f.request("alice", "GET", "/email/1/body", "")
	body := r.Body.String()
	if r.Code != 200 || !strings.Contains(body, "stalebodytoken") {
		t.Fatal("cached body not served", r.Code, body)
	}
	if strings.Contains(body, "utm_source") || !strings.Contains(body, "id=7") {
		t.Fatal("tracking parameter kept or real one lost:", body)
	}
	if strings.Contains(body, "t.example/p.gif") {
		t.Fatal("tracking pixel kept:", body)
	}
}
