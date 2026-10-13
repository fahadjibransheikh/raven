package imap

import (
	"context"
	"errors"
	"strings"
	"testing"

	goimap "github.com/emersion/go-imap/v2"

	"github.com/fahadjibransheikh/raven/internal/mail/message"
)

func TestFetchBodyRefusesMessagesOverTheLimitWithoutReadingThem(t *testing.T) {
	cfg, _ := startRecordedIMAPServer(t, goimap.CapSet{goimap.CapIMAP4rev1: {}})
	client, err := NewClient(context.Background(), cfg, "secret")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()
	raw := "From: Rosa Vidal <rosa@example.com>\r\nSubject: Site photos\r\n\r\n" + strings.Repeat("photo data ", 400) + "\r\n"
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

	body, err := client.FetchBody(context.Background(), "INBOX", 1)
	if err != nil || string(body) != raw {
		t.Fatalf("FetchBody() = %d bytes, %v; want the whole message", len(body), err)
	}

	limited := message.WithDownloadLimit(context.Background(), 1024)
	_, err = client.FetchBody(limited, "INBOX", 1)
	var tooLarge *message.TooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.Size != int64(len(raw)) || tooLarge.Limit != 1024 || !tooLarge.CanDownloadFully() {
		t.Fatalf("FetchBody() over the limit error = %#v", err)
	}
	// The rest of the message was never read, so the session can't be reused.
	if _, err := client.FetchBody(context.Background(), "INBOX", 1); err == nil {
		t.Fatal("the session was reused after a refused message")
	}
}
