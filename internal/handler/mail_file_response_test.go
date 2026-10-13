package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/storage"
)

func TestServeMailFileOnlyShowsImagesInPlace(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, dest string
		inline                  bool
		wantType, wantDisp      string
	}{
		{"html part", "text/html; charset=utf-8", "document", true, "application/octet-stream", "attachment"},
		{"xhtml part", "application/xhtml+xml", "document", true, "application/octet-stream", "attachment"},
		{"svg opened as a page", "image/svg+xml", "document", true, "application/octet-stream", "attachment"},
		{"svg without fetch metadata", "image/svg+xml", "", true, "application/octet-stream", "attachment"},
		{"svg in an img", "image/svg+xml", "image", true, "image/svg+xml", "inline"},
		{"png opened as a page", "image/png", "document", true, "image/png", "inline"},
		{"png declared as jpg", "image/jpg", "image", true, "image/png", "inline"},
		{"html declared as png", "image/png", "document", true, "application/octet-stream", "attachment"},
		{"malformed type", "image/png;;=", "image", true, "application/octet-stream", "attachment"},
		{"download of an image", "image/png", "document", false, "application/octet-stream", "attachment"},
		{"download of html", "text/html", "document", false, "application/octet-stream", "attachment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/file", nil)
			if tc.dest != "" {
				req.Header.Set("Sec-Fetch-Dest", tc.dest)
			}
			rec := httptest.NewRecorder()
			// The file's bytes decide whether it is an image, not the declared type.
			content := "<svg onload=alert(1)>"
			if strings.HasPrefix(tc.name, "png") {
				content = pngMagic
			}
			serveMailFile(rec, req, strings.NewReader(content), "part.bin", tc.contentType, tc.inline)
			if got := rec.Header().Get("Content-Type"); got != tc.wantType {
				t.Fatalf("Content-Type = %q, want %q", got, tc.wantType)
			}
			if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, tc.wantDisp+";") {
				t.Fatalf("Content-Disposition = %q, want %s", got, tc.wantDisp)
			}
			if got := rec.Header().Get("Content-Security-Policy"); !strings.HasSuffix(got, "; sandbox") || !strings.Contains(got, "default-src 'none'") {
				t.Fatalf("Content-Security-Policy = %q, want a sandbox with no default sources", got)
			}
			if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("response may be type-sniffed")
			}
		})
	}
}

func TestServeMailFileQuotesFilename(t *testing.T) {
	rec := httptest.NewRecorder()
	serveMailFile(rec, httptest.NewRequest(http.MethodGet, "/file", nil), strings.NewReader("x"), `a"; filename=evil.html`, "text/plain", false)
	got := rec.Header().Get("Content-Disposition")
	if got != `attachment; filename="a\"; filename=evil.html"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
}

func TestRemoteAssetContentTypeFollowsStoredExtension(t *testing.T) {
	for filename, want := range map[string]string{
		"0123456789abcdef.png": "image/png",
		"0123456789abcdef.SVG": "image/svg+xml",
		"0123456789abcdef":     "application/octet-stream",
	} {
		if got := remoteAssetContentType(filename); got != want {
			t.Fatalf("remoteAssetContentType(%q) = %q, want %q", filename, got, want)
		}
	}
}

// A sender can add an inline XHTML part with a Content-ID and link to it with
// cid:. The rewritten link points at the inline-content route, so opening it
// must never render the part as a page on the app's origin.
func TestOwnedInlineContentDoesNotRenderSenderHTML(t *testing.T) {
	f := newOwnedMessageContentFixture(t)
	part := filepath.Join(t.TempDir(), "invoice.xhtml")
	if err := os.WriteFile(part, []byte(`<script>fetch("/api/accounts")</script>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.h.withUserDB(t.Context(), "alice", func(db *storage.DB) error {
		_, err := db.Write().Exec(`INSERT INTO attachments(message_id,filename,content_type,size_bytes,content_id,inline,storage_path) VALUES(1,'invoice.xhtml','application/xhtml+xml',40,'invoice',1,?)`, part)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	req := ownedContentRequest("alice", "/api/inline-content/1/invoice", "")
	req.SetPathValue("contentID", "invoice")
	req.Header.Set("Sec-Fetch-Dest", "document")
	rec := httptest.NewRecorder()
	f.h.handleUserInlineContent(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Fatalf("Content-Disposition = %q, want attachment", got)
	}
	if !strings.HasSuffix(rec.Header().Get("Content-Security-Policy"), "; sandbox") {
		t.Fatal("inline content is not sandboxed")
	}
}
