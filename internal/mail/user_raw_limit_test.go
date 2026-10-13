package mail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/mail/message"
)

// Messages the reader didn't ask for stop at the automatic download limit,
// whether or not the provider says the size first.
func TestProviderRawDownloadsStopAtTheDownloadLimit(t *testing.T) {
	raw := "From: Rosa Vidal <rosa@example.com>\r\nSubject: Site photos\r\n\r\n" + strings.Repeat("photo data ", 400) + "\r\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/$value") {
			w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
			_, _ = w.Write([]byte(raw))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "m1", "raw": base64.URLEncoding.EncodeToString([]byte(raw))})
	}))
	t.Cleanup(server.Close)
	previousGmail, previousGraph := gmailAPIBaseURL, outlookGraphBaseURL
	gmailAPIBaseURL, outlookGraphBaseURL = server.URL, server.URL
	t.Cleanup(func() { gmailAPIBaseURL, outlookGraphBaseURL = previousGmail, previousGraph })

	limited := message.WithDownloadLimit(context.Background(), 1024)
	for name, fetch := range map[string]func(context.Context) ([]byte, error){
		"gmail":   func(ctx context.Context) ([]byte, error) { return getUserGmailRaw(ctx, "token", "m1") },
		"outlook": func(ctx context.Context) ([]byte, error) { return getUserOutlookRaw(ctx, "token", "m1") },
	} {
		if got, err := fetch(context.Background()); err != nil || string(got) != raw {
			t.Fatalf("%s: full download = %d bytes, %v", name, len(got), err)
		}
		_, err := fetch(limited)
		var tooLarge *message.TooLargeError
		if !errors.As(err, &tooLarge) || tooLarge.Limit != 1024 || !tooLarge.CanDownloadFully() {
			t.Fatalf("%s: limited download error = %#v", name, err)
		}
		if name == "outlook" && tooLarge.Size != int64(len(raw)) {
			t.Fatalf("outlook: size = %d, want %d from Content-Length", tooLarge.Size, len(raw))
		}
	}
}
