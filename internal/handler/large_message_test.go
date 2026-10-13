package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/mail/message"
)

// The shared server holds back a message over the automatic limit the same
// way: the reader explains why and downloads it when asked.
func TestSharedLargeMessageWaitsUntilTheReaderAsks(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	raw := "From: Rosa Vidal <rosa@example.com>\r\nSubject: Site photos\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nPhotos from the site visit attached.\r\n" +
		"--b\r\nContent-Type: image/jpeg; name=site.jpg\r\nContent-Disposition: attachment; filename=site.jpg\r\nContent-Transfer-Encoding: base64\r\n\r\n" +
		strings.Repeat(strings.Repeat("A", 76)+"\r\n", 26<<20/78) + "--b--\r\n"
	rawPath := filepath.Join(t.TempDir(), "raw.eml")
	if err := os.WriteFile(rawPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().ExecContext(t.Context(), `INSERT INTO folders (id, account_id, remote_id, name, role) VALUES ('owner-inbox', 'victim-account', 'INBOX', 'Inbox', 'inbox')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().ExecContext(t.Context(), `INSERT INTO messages (id, account_id, internet_message_id, thread_id, subject, from_email, raw_path) VALUES (7, 'victim-account', '<photos@example.com>', 'photos', 'Site photos', 'rosa@example.com', ?)`, rawPath); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().ExecContext(t.Context(), `INSERT INTO message_folder_state (message_id, folder_id, remote_uid) VALUES (7, 'owner-inbox', 7)`); err != nil {
		t.Fatal(err)
	}
	body := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/email/7/body", nil)
		req.SetPathValue("id", "7")
		rec := httptest.NewRecorder()
		h.handleEmailBody(rec, ownerRequest(req))
		return rec
	}
	if rec := body(); rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), `"kind":"large"`) || !strings.Contains(rec.Body.String(), "wasn't downloaded automatically") {
		t.Fatalf("large message body = %d %s", rec.Code, rec.Body.String())
	}
	full := func(request func(*http.Request) *http.Request) int {
		req := httptest.NewRequest(http.MethodPost, "/api/messages/7/download-full", nil)
		req.SetPathValue("id", "7")
		rec := httptest.NewRecorder()
		h.handleDownloadFullBody(rec, request(req))
		return rec.Code
	}
	if code := full(attackerRequest); code == http.StatusNoContent {
		t.Fatal("another person downloaded the message")
	}
	if code := full(ownerRequest); code != http.StatusNoContent {
		t.Fatalf("full download = %d", code)
	}
	if rec := body(); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Photos from the site visit attached.") {
		t.Fatalf("downloaded message body = %d %s", rec.Code, rec.Body.String())
	}
}

func TestMessageTooLargeExplanations(t *testing.T) {
	for _, tc := range []struct {
		err        error
		kind, text string
	}{
		{&message.TooLargeError{Size: 30 << 20, Limit: 25 << 20}, "large", "This message is 30 MB, so it wasn't downloaded automatically."},
		{&message.TooLargeError{Limit: 25 << 20}, "large", "This message is larger than 25 MB, so it wasn't downloaded automatically."},
		{&message.TooLargeError{Size: 80 << 20, Limit: 25 << 20}, "oversize", "This message is 80 MB. Raven can show messages up to 64 MB."},
		{&message.TooLargeError{Limit: 64 << 20}, "oversize", "This message is larger than the 64 MB Raven can show."},
	} {
		kind, text, _ := messageTooLargeFailure(tc.err.(*message.TooLargeError))
		if kind != tc.kind || text != tc.text {
			t.Errorf("%v = %q %q, want %q %q", tc.err, kind, text, tc.kind, tc.text)
		}
	}
}
