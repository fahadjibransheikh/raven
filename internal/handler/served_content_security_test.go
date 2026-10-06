package handler

import (
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/storage"
)

const pngMagic = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"

func insertServedAttachment(t *testing.T, db *storage.DB, messageID int64, filename, contentType, contentID string, body []byte) int64 {
	t.Helper()
	path := filepath.Join(t.TempDir(), "att.bin")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	res, err := db.Write().ExecContext(t.Context(), `
		INSERT INTO attachments (message_id, filename, content_type, content_id, size_bytes, storage_path, inline)
		VALUES (?, ?, ?, ?, ?, ?, 1)`, messageID, filename, contentType, contentID, len(body), path)
	if err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func requireIsolated(t *testing.T, rec *httptest.ResponseRecorder, wantDisposition string) {
	t.Helper()
	if csp := rec.Header().Get("Content-Security-Policy"); csp != "sandbox; default-src 'none'" {
		t.Errorf("Content-Security-Policy = %q, want sandbox; default-src 'none'", csp)
	}
	if v := rec.Header().Get("X-Content-Type-Options"); v != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", v)
	}
	if got := strings.SplitN(rec.Header().Get("Content-Disposition"), ";", 2)[0]; got != wantDisposition {
		t.Errorf("Content-Disposition = %q, want %q", rec.Header().Get("Content-Disposition"), wantDisposition)
	}
}

func TestInlineContentIsIsolatedAndNeverServedAsActiveType(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fx := insertVictimReadableMessage(t, h, db)
	cases := []struct {
		name, contentType, cid string
		body                   []byte
		wantType, wantDisp     string
	}{
		{"html", "text/html", "h", []byte("<script>alert(1)</script>"), "application/octet-stream", "attachment"},
		{"xhtml", "application/xhtml+xml", "x", []byte("<html xmlns='http://www.w3.org/1999/xhtml'><script>1</script></html>"), "application/octet-stream", "attachment"},
		{"svg", "image/svg+xml", "s", []byte("<svg xmlns='http://www.w3.org/2000/svg' onload='alert(1)'><script>1</script></svg>"), "image/svg+xml", "attachment"},
		{"png", "image/png", "p", []byte(pngMagic), "image/png", "inline"},
		{"png-lying", "image/png", "l", []byte("<html><script>1</script></html>"), "application/octet-stream", "attachment"},
		{"html-lying-as-png", "text/html", "q", []byte(pngMagic), "image/png", "inline"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			insertServedAttachment(t, db, fx.messageID, "f", tc.contentType, tc.cid, tc.body)
			req := httptest.NewRequest(http.MethodGet, "/api/inline-content/101/"+tc.cid, nil)
			req.SetPathValue("messageID", strconv.FormatInt(fx.messageID, 10))
			req.SetPathValue("contentID", tc.cid)
			rec := httptest.NewRecorder()
			h.handleInlineContent(rec, ownerRequest(req))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			requireIsolated(t, rec, tc.wantDisp)
			if got := rec.Header().Get("Content-Type"); got != tc.wantType {
				t.Errorf("Content-Type = %q, want %q", got, tc.wantType)
			}
		})
	}
}

func TestAttachmentPreviewRequiresRealRasterImage(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fx := insertVictimReadableMessage(t, h, db)
	preview := func(id int64) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/attachments/x/preview", nil)
		req.SetPathValue("id", strconv.FormatInt(id, 10))
		rec := httptest.NewRecorder()
		h.handleAttachmentPreview(rec, ownerRequest(req))
		return rec
	}
	// Filename says .png but the sender declared HTML: must not be previewed.
	if rec := preview(insertServedAttachment(t, db, fx.messageID, "x.png", "text/html", "", []byte("<script>1</script>"))); rec.Code != http.StatusNotFound {
		t.Errorf("html named .png: status = %d, want 404", rec.Code)
	}
	if rec := preview(insertServedAttachment(t, db, fx.messageID, "x.svg", "image/svg+xml", "", []byte("<svg/>"))); rec.Code != http.StatusNotFound {
		t.Errorf("svg: status = %d, want 404", rec.Code)
	}
	rec := preview(insertServedAttachment(t, db, fx.messageID, "x.png", "image/png", "", []byte(pngMagic)))
	if rec.Code != http.StatusOK {
		t.Fatalf("png: status = %d, want 200", rec.Code)
	}
	requireIsolated(t, rec, "inline")
}

func TestAttachmentDownloadIsIsolated(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fx := insertVictimReadableMessage(t, h, db)
	id := insertServedAttachment(t, db, fx.messageID, `a".html"; filename*=UTF-8''evil.exe`, "text/html", "", []byte("<script>1</script>"))
	req := httptest.NewRequest(http.MethodGet, "/api/attachments/x/download", nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	h.handleAttachmentDownload(rec, ownerRequest(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	requireIsolated(t, rec, "attachment")
	_, params, err := mime.ParseMediaType(rec.Header().Get("Content-Disposition"))
	if err != nil || len(params) != 1 || params["filename"] != `a".html"; filename*=UTF-8''evil.exe` {
		t.Errorf("filename parameter injection: %q (params %v, err %v)", rec.Header().Get("Content-Disposition"), params, err)
	}
}

func TestRemoteAssetIsIsolated(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	fx := insertVictimReadableMessage(t, h, db)
	p, err := h.blobStore.StoreRemoteAsset("victim-account", fx.messageID, "https://evil.example/x.svg", []byte("<svg xmlns='http://www.w3.org/2000/svg'><script>1</script></svg>"))
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(p)
	req := httptest.NewRequest(http.MethodGet, "/api/remote-assets/101/"+name, nil)
	req.SetPathValue("messageID", strconv.FormatInt(fx.messageID, 10))
	req.SetPathValue("filename", name)
	rec := httptest.NewRecorder()
	h.handleRemoteAsset(rec, ownerRequest(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	requireIsolated(t, rec, "attachment")
}
