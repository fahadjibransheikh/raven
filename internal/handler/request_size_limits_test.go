package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/mail/message"
)

func TestComposeMessageSizeMeasuresFilesInsteadOfClaimedSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upload.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse 20 MB file: real size on Stat, no disk use.
	if err := f.Truncate(20 << 20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	one := message.OutgoingAttachment{Filename: "a.bin", Path: path, Size: 1}
	if err := validateComposeMessageSize([]message.OutgoingAttachment{one}, "body", ""); err != nil {
		t.Fatal("single 20 MB attachment rejected", err)
	}
	// The same upload repeated under a claimed size of 1 byte must still count in full.
	if err := validateComposeMessageSize([]message.OutgoingAttachment{one, one}, "body", ""); err == nil {
		t.Fatal("repeated attachment with a fake size passed the size check")
	}
}

func TestUISettingsRejectsOversizedBodiesAndStoredSettings(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	huge := `{"x":"` + strings.Repeat("a", 300<<10) + `"}`
	if rec := patchUISettings(h, huge); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized body status = %d, want 400", rec.Code)
	}
	// Each request fits, but distinct keys accumulate past the stored cap.
	half := strings.Repeat("b", 150<<10)
	if rec := patchUISettings(h, `{"k1":"`+half+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("first save status = %d", rec.Code)
	}
	if rec := patchUISettings(h, `{"k2":"`+half+`"}`); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("accumulated save status = %d, want 413", rec.Code)
	}
	if _, ok := db.GetUISettings(t.Context(), "owner")["k2"]; ok {
		t.Fatal("oversized settings were stored")
	}
	if rec := patchUISettings(h, `{"theme":"light"}`); rec.Code != http.StatusOK {
		t.Fatalf("normal save after rejection status = %d", rec.Code)
	}
}
