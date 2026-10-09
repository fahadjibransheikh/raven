package imap

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	goimap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/cristianadrielbraun/gofer/internal/models"
	"github.com/cristianadrielbraun/gofer/internal/storage"
)

func TestSyncCapturesListUnsubscribeHeaders(t *testing.T) {
	memServer := imapmemserver.New()
	user := imapmemserver.NewUser("user@example.com", "secret")
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	memServer.AddUser(user)
	server := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memServer.NewSession(), nil, nil
		},
		InsecureAuth: true,
		Caps:         goimap.CapSet{goimap.CapIMAP4rev1: {}, goimap.CapIMAP4rev2: {}},
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	host, portString, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portString)
	client, err := NewClient(context.Background(), &models.AccountConfig{
		AccountID: "acc", IMAPHost: host, IMAPPort: port, IMAPTLSMode: "plaintext",
		IMAPAllowPlaintext: true, AuthMethod: "plain", Username: "user@example.com",
	}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	raw := []byte("From: news@example.com\r\nTo: user@example.com\r\nMessage-ID: <lu@example.com>\r\nSubject: News\r\n" +
		"List-Unsubscribe: <mailto:unsub@example.com>,\r\n <https://example.com/u/1>\r\nList-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n\r\nBody")
	if _, err := client.AppendMessage(context.Background(), "INBOX", raw, nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	check := func(name string, msgs []storage.SyncMessage) {
		t.Helper()
		if len(msgs) != 1 {
			t.Fatalf("%s: got %d messages, want 1", name, len(msgs))
		}
		if got := msgs[0].ListUnsubscribe; got != "<mailto:unsub@example.com>, <https://example.com/u/1>" {
			t.Errorf("%s: ListUnsubscribe = %q", name, got)
		}
		if got := msgs[0].ListUnsubscribePost; got != "List-Unsubscribe=One-Click" {
			t.Errorf("%s: ListUnsubscribePost = %q", name, got)
		}
	}
	var full []storage.SyncMessage
	if _, err := client.SyncFolder(context.Background(), "inbox", "INBOX", FolderSyncOptions{ChunkSize: 10}, func(m []storage.SyncMessage) error {
		full = append(full, m...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	check("SyncFolder", full)

	var inc []storage.SyncMessage
	if _, err := client.SyncFolderIncremental(context.Background(), "inbox", "INBOX", 0, 0, func(m []storage.SyncMessage) error {
		inc = append(inc, m...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	check("SyncFolderIncremental", inc)
}
