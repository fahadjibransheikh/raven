package storage

import (
	"testing"
	"time"
)

func TestFormatRelativeDateUses24HourClockToday(t *testing.T) {
	loc := time.FixedZone("EDT", -4*3600)
	now := time.Date(2026, 10, 8, 22, 0, 0, 0, loc)
	for _, tc := range []struct {
		t    time.Time
		want string
	}{
		{time.Date(2026, 10, 8, 21, 1, 0, 0, loc), "21:01"},
		{time.Date(2026, 10, 8, 9, 5, 0, 0, loc), "09:05"},
		{time.Date(2026, 10, 7, 21, 1, 0, 0, loc), "Yesterday"},
		{time.Date(2026, 1, 2, 21, 1, 0, 0, loc), "Jan 2"},
		{time.Date(2025, 1, 2, 21, 1, 0, 0, loc), "Jan 2, 2025"},
	} {
		if got := formatRelativeDate(tc.t, now, loc); got != tc.want {
			t.Errorf("formatRelativeDate(%v) = %q, want %q", tc.t, got, tc.want)
		}
	}
}
