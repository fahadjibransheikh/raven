package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func TestComposeUploadStopsReadingAtTheCap(t *testing.T) {
	h, _ := newAccountOwnershipTestHandler(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("attachment", "big.bin")
	_, _ = part.Write(make([]byte, 80<<20)) // far past the 25 MiB attachment limit
	_ = mw.Close()

	reader := &countingReader{r: &body}
	req := httptest.NewRequest(http.MethodPost, "/api/compose/attachments", reader)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.handleComposeAttachmentUpload(rec, ownerRequest(req))

	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != http.StatusBadRequest || resp["error"] != "attachment is too large" {
		t.Fatalf("status = %d body = %q, want 400 attachment is too large", rec.Code, rec.Body.String())
	}
	if limit := composeAttachmentMaxBytes + multipartOverheadBytes; reader.n > limit+64<<10 {
		t.Fatalf("handler read %d bytes of an 80 MiB upload, want at most about %d", reader.n, limit)
	}
}
