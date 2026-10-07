package storage

import "testing"

func TestThreadOrderDefaultsToNewestFirst(t *testing.T) {
	if got := defaultUISettings()["thread_order"]; got != "newest_first" {
		t.Errorf("thread_order default = %q, want newest_first", got)
	}
}
