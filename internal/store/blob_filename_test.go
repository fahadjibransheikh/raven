package store

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStoreAttachmentLongFilenameIsShortenedKeepingExtension(t *testing.T) {
	for _, test := range []struct{ name, filename, ext string }{
		{"ascii", strings.Repeat("a", 300) + ".pdf", ".pdf"},
		{"multibyte stem", strings.Repeat("é", 200) + ".docx", ".docx"},
		{"cjk stem cut mid-rune", "x" + strings.Repeat("日", 100) + ".txt", ".txt"},
		{"no extension", strings.Repeat("b", 400), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := NewBlobStore(t.TempDir())
			p, err := s.StoreAttachment(t.Context(), "acct", 1, 1, test.filename, bytes.NewBufferString("data"))
			if err != nil {
				t.Fatalf("StoreAttachment() error = %v", err)
			}
			if data, err := os.ReadFile(p); err != nil || string(data) != "data" {
				t.Fatalf("stored file unreadable: %q, %v", data, err)
			}
			name := sanitizeFilename(test.filename)
			if len(name) > 150 || !utf8.ValidString(name) || !strings.HasSuffix(name, test.ext) {
				t.Fatalf("sanitizeFilename() = %q (%d bytes)", name, len(name))
			}
		})
	}
}
