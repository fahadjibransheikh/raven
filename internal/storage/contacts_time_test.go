package storage

import (
	"testing"
	"time"
)

func TestFormatContactTimeReadsTimeStringValues(t *testing.T) {
	for _, raw := range []string{
		"2026-10-20 08:36:54.897451 +0000 UTC",
		"2026-10-20 08:36:54.897451 +0000 UTC m=+3.141592654",
		"2026-10-20T08:36:54Z",
	} {
		if got := formatContactTime(raw, time.UTC); got != "Oct 20, 2026" {
			t.Fatalf("formatContactTime(%q) = %q, want Oct 20, 2026", raw, got)
		}
	}
}
