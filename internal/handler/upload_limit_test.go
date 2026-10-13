package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Uploads larger than Gofer accepts are refused instead of being written to
// temporary files whatever their size.
func TestSharedUploadsRefuseOversizedBodies(t *testing.T) {
	h, _ := newAccountOwnershipTestHandler(t)
	var consumed int64
	oversized := func(field, filename string, size int64) *http.Request {
		consumed = 0
		var head bytes.Buffer
		writer := multipart.NewWriter(&head)
		if _, err := writer.CreateFormFile(field, filename); err != nil {
			t.Fatal(err)
		}
		tail := "\r\n--" + writer.Boundary() + "--\r\n"
		body := &countingReader{r: io.MultiReader(bytes.NewReader(head.Bytes()), io.LimitReader(zeroReader{}, size), strings.NewReader(tail)), n: &consumed}
		req := httptest.NewRequest(http.MethodPost, "/", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		return req
	}

	rec := httptest.NewRecorder()
	h.handleComposeAttachmentUpload(rec, oversized("attachment", "big.bin", composeAttachmentMaxBytes+(64<<20)))
	if rec.Code != http.StatusBadRequest || consumed > composeAttachmentMaxBytes+(4<<20) {
		t.Fatalf("oversized attachment upload = %d %q after reading %d bytes", rec.Code, rec.Body.String(), consumed)
	}
	rec = httptest.NewRecorder()
	h.handleImportContacts(rec, oversized("vcard", "big.vcf", contactImportMaxBytes+(64<<20)))
	if rec.Code != http.StatusBadRequest || consumed > contactImportMaxBytes+(4<<20) {
		t.Fatalf("oversized contact import = %d %q after reading %d bytes", rec.Code, rec.Body.String(), consumed)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

type countingReader struct {
	r io.Reader
	n *int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	*c.n += int64(n)
	return n, err
}

func TestComposeUploadStopsReadingAtTheCap(t *testing.T) {
	h, _ := newAccountOwnershipTestHandler(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("attachment", "big.bin")
	_, _ = part.Write(make([]byte, 80<<20)) // far past the 25 MiB attachment limit
	_ = mw.Close()

	var consumed int64
	reader := &countingReader{r: &body, n: &consumed}
	req := httptest.NewRequest(http.MethodPost, "/api/compose/attachments", reader)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.handleComposeAttachmentUpload(rec, ownerRequest(req))

	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != http.StatusBadRequest || resp["error"] != "attachment is too large" {
		t.Fatalf("status = %d body = %q, want 400 attachment is too large", rec.Code, rec.Body.String())
	}
	if limit := composeAttachmentMaxBytes + multipartOverheadBytes; consumed > limit+64<<10 {
		t.Fatalf("handler read %d bytes of an 80 MiB upload, want at most about %d", consumed, limit)
	}
}
