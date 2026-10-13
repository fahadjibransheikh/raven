package handler

import (
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// Files taken from a message (attachments, inline parts, saved remote images)
// are chosen by the sender but served from the app's own origin. Opened
// directly in a tab, an HTML or SVG part would run with the reader's session,
// so every such response is sandboxed, is never type-sniffed, and is shown in
// place only when it is an image. Everything else is downloaded.
const mailFileContentSecurityPolicy = "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; frame-ancestors 'self'; sandbox"

// mailFileInlineTypes are the raster images shown in place. The type is judged
// by the file's bytes, never by what the sender declared.
var mailFileInlineTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
	"image/avif": true,
}

var remoteAssetTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
	".ico":  "image/x-icon",
	".bmp":  "image/bmp",
}

func mailFileMediaType(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "application/octet-stream"
	}
	if mediaType == "image/jpg" {
		return "image/jpeg"
	}
	return mediaType
}

// remoteAssetContentType names a saved remote image by the extension it was
// stored with. Files saved without one were not recognized as images.
func remoteAssetContentType(filename string) string {
	if contentType, ok := remoteAssetTypes[strings.ToLower(filepath.Ext(filename))]; ok {
		return contentType
	}
	return "application/octet-stream"
}

// sniffMailFileType names a file's type from its first bytes and puts the
// reader back at the start.
func sniffMailFileType(content io.ReadSeeker) string {
	head := make([]byte, 512)
	n, _ := io.ReadFull(content, head)
	_, _ = content.Seek(0, io.SeekStart)
	head = head[:n]
	if len(head) >= 12 && string(head[4:8]) == "ftyp" && (string(head[8:12]) == "avif" || string(head[8:12]) == "avis") {
		return "image/avif"
	}
	return strings.ToLower(strings.Split(http.DetectContentType(head), ";")[0])
}

// serveMailFile writes a file taken from a message. With inline set, a file
// that really is a raster image (judged by its bytes) is shown in place, and an
// SVG only where the browser loads it as an image; anything else, and every
// download, is sent as opaque bytes to save. BMP and ICO files keep their image
// type so an <img> still renders them, but are never shown as pages. The caller
// sets Cache-Control.
func serveMailFile(w http.ResponseWriter, r *http.Request, content io.ReadSeeker, filename, contentType string, inline bool) {
	mediaType, disposition := "application/octet-stream", "attachment"
	if inline {
		sniffed := sniffMailFileType(content)
		switch declared := mailFileMediaType(contentType); {
		case mailFileInlineTypes[sniffed]:
			mediaType, disposition = sniffed, "inline"
		case sniffed == "image/bmp" || sniffed == "image/x-icon":
			mediaType = sniffed
		case declared == "image/svg+xml" && r.Header.Get("Sec-Fetch-Dest") == "image":
			mediaType, disposition = declared, "inline"
		}
	}
	header := w.Header()
	header.Set("Content-Type", mediaType)
	header.Set("Content-Security-Policy", mailFileContentSecurityPolicy)
	// A second, stricter policy: both apply, so nothing here can load or run.
	header.Add("Content-Security-Policy", "sandbox; default-src 'none'")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Add("Vary", "Sec-Fetch-Dest")
	if value := mime.FormatMediaType(disposition, map[string]string{"filename": strings.TrimSpace(filename)}); value != "" && strings.TrimSpace(filename) != "" {
		header.Set("Content-Disposition", value)
	} else {
		header.Set("Content-Disposition", disposition)
	}
	http.ServeContent(w, r, "", time.Time{}, content)
}
