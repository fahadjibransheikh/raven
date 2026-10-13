package imap

import (
	"context"
	"strings"
	"testing"

	goimap "github.com/emersion/go-imap/v2"

	"github.com/cristianadrielbraun/gofer/internal/storage"
)

func TestSyncAndFetchSizesReportMessageSizes(t *testing.T) {
	cfg, _ := startRecordedIMAPServer(t, goimap.CapSet{goimap.CapIMAP4rev1: {}})
	client, err := NewClient(context.Background(), cfg, "secret")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()
	var raws []string
	for i, body := range []string{"See you at noon.", strings.Repeat("plan ", 300)} {
		raw := "From: Rosa Vidal <rosa@example.com>\r\nSubject: Note " + string(rune('A'+i)) + "\r\nMessage-ID: <note" + string(rune('a'+i)) + "@example.com>\r\n\r\n" + body + "\r\n"
		raws = append(raws, raw)
		appendCmd := client.client.Append("INBOX", int64(len(raw)), nil)
		if _, err := appendCmd.Write([]byte(raw)); err != nil {
			t.Fatal(err)
		}
		if err := appendCmd.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := appendCmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}

	var synced []storage.SyncMessage
	if _, err := client.SyncFolder(context.Background(), "folder", "INBOX", FolderSyncOptions{ChunkSize: 50}, func(msgs []storage.SyncMessage) error {
		synced = append(synced, msgs...)
		return nil
	}); err != nil {
		t.Fatalf("SyncFolder() error = %v", err)
	}
	if len(synced) != 2 {
		t.Fatalf("synced %d messages", len(synced))
	}
	for _, msg := range synced {
		if want := int64(len(raws[msg.RemoteUID-1])); msg.SizeBytes != want {
			t.Fatalf("UID %d size = %d, want %d", msg.RemoteUID, msg.SizeBytes, want)
		}
	}

	sizes, err := client.FetchSizes(context.Background(), "INBOX", []uint32{1, 2, 9}, 0)
	if err != nil {
		t.Fatalf("FetchSizes() error = %v", err)
	}
	if len(sizes) != 2 || sizes[1] != int64(len(raws[0])) || sizes[2] != int64(len(raws[1])) {
		t.Fatalf("FetchSizes() = %v", sizes)
	}
}
