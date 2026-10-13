package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type cacheFixture struct {
	t    *testing.T
	base string
	now  time.Time
}

func newCacheFixture(t *testing.T) *cacheFixture {
	return &cacheFixture{t: t, base: t.TempDir(), now: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
}

// write creates a file under account/messages/<id>/ and dates it age ago.
func (f *cacheFixture) write(account string, id string, rel string, size int, age time.Duration) string {
	f.t.Helper()
	p := filepath.Join(f.base, account, "messages", id, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o600); err != nil {
		f.t.Fatal(err)
	}
	when := f.now.Add(-age)
	if err := os.Chtimes(p, when, when); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *cacheFixture) exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

const day = 24 * time.Hour

func TestSweepRelayedImagesRemovesOnlyOldCacheEntries(t *testing.T) {
	f := newCacheFixture(t)
	oldCache := f.write("acc", "1", "remote_assets/web/aaaa.png", 10, 30*day)
	freshCache := f.write("acc", "1", "remote_assets/web/bbbb.png", 10, 1*day)
	// Saved images, bodies and attachments are never touched, however old.
	saved := f.write("acc", "1", "remote_assets/cccc.png", 10, 400*day)
	body := f.write("acc", "1", "body.html", 10, 400*day)
	attachment := f.write("acc", "1", "attachments/7-big.bin", 10, 400*day)
	attachmentLookalike := f.write("acc", "1", "attachments/remote_assets/web/dddd.png", 10, 400*day)
	managedOld := f.write("acc", "2", "0123abcd/remote_assets/web/eeee.png", 10, 20*day)

	result, err := NewBlobStore(f.base).SweepRelayedImages(context.Background(), f.now, RelayedImageSweep{MaxAge: 14 * day})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 2 || result.Bytes != 20 {
		t.Fatalf("result = %+v, want 2 files / 20 bytes", result)
	}
	for p, want := range map[string]bool{oldCache: false, managedOld: false, freshCache: true, saved: true, body: true, attachment: true, attachmentLookalike: true} {
		if f.exists(p) != want {
			t.Errorf("%s exists = %v, want %v", filepath.Base(p), !want, want)
		}
	}
}

func TestSweepRelayedImagesEvictsLeastRecentlyUsedBeyondTheCap(t *testing.T) {
	f := newCacheFixture(t)
	oldest := f.write("acc", "1", "remote_assets/web/1.png", 400, 5*day)
	middle := f.write("acc", "2", "remote_assets/web/2.png", 400, 3*day)
	newest := f.write("acc", "3", "remote_assets/web/3.png", 400, 1*day)
	saved := f.write("acc", "1", "remote_assets/saved.png", 5000, 9*day)

	result, err := NewBlobStore(f.base).SweepRelayedImages(context.Background(), f.now, RelayedImageSweep{MaxAge: 14 * day, MaxBytes: 800})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 || f.exists(oldest) || !f.exists(middle) || !f.exists(newest) {
		t.Fatalf("result %+v; oldest kept=%v middle kept=%v newest kept=%v", result, f.exists(oldest), f.exists(middle), f.exists(newest))
	}
	if !f.exists(saved) {
		t.Fatal("a saved image counted toward the cap and was evicted")
	}
	// Within the cap nothing more goes.
	if again, _ := NewBlobStore(f.base).SweepRelayedImages(context.Background(), f.now, RelayedImageSweep{MaxBytes: 800}); again.Files != 0 {
		t.Fatalf("second sweep removed %+v", again)
	}
}

func TestSweepRelayedImagesRemovesTheCacheOfMessagesThatNoLongerExist(t *testing.T) {
	f := newCacheFixture(t)
	gone := f.write("acc", "7", "remote_assets/web/1.png", 10, 1*day)
	goneSaved := f.write("acc", "7", "remote_assets/saved.png", 10, 1*day)
	kept := f.write("acc", "8", "remote_assets/web/2.png", 10, 1*day)
	asked := map[int64]int{}
	result, err := NewBlobStore(f.base).SweepRelayedImages(context.Background(), f.now, RelayedImageSweep{
		MaxAge: 14 * day,
		MessageExists: func(_ context.Context, account string, id int64) (bool, error) {
			asked[id]++
			return id != 7, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 || f.exists(gone) || !f.exists(kept) {
		t.Fatalf("result %+v; gone kept=%v kept kept=%v", result, f.exists(gone), f.exists(kept))
	}
	// Only the cache goes: the message's saved images are its own business.
	if !f.exists(goneSaved) {
		t.Fatal("a saved image of a deleted message was swept")
	}
	if asked[7] != 1 || asked[8] != 1 {
		t.Fatalf("existence checks = %v, want one per message", asked)
	}
}

func TestSweepRelayedImagesClearsAbandonedTempFilesOnly(t *testing.T) {
	f := newCacheFixture(t)
	stale := f.write("acc", "1", "remote_assets/web/.tmp-111", 10, 3*time.Hour)
	inProgress := f.write("acc", "1", "remote_assets/web/.tmp-222", 10, time.Minute)
	if _, err := NewBlobStore(f.base).SweepRelayedImages(context.Background(), f.now, RelayedImageSweep{}); err != nil {
		t.Fatal(err)
	}
	if f.exists(stale) || !f.exists(inProgress) {
		t.Fatalf("stale kept=%v in-progress kept=%v", f.exists(stale), f.exists(inProgress))
	}
}

func TestSweepRelayedImagesWithNothingStoredIsHarmless(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-created")
	if result, err := NewBlobStore(missing).SweepRelayedImages(context.Background(), time.Now(), RelayedImageSweep{MaxAge: day}); err != nil || result.Files != 0 {
		t.Fatalf("result %+v, err %v", result, err)
	}
}

func TestTouchRelayedImageRefreshesOnlyStaleMarks(t *testing.T) {
	f := newCacheFixture(t)
	old := f.write("acc", "1", "remote_assets/web/a.png", 1, 5*day)
	recent := f.write("acc", "1", "remote_assets/web/b.png", 1, 10*time.Minute)
	TouchRelayedImage(old, f.now)
	TouchRelayedImage(recent, f.now)
	oldInfo, _ := os.Stat(old)
	recentInfo, _ := os.Stat(recent)
	if !oldInfo.ModTime().Equal(f.now) {
		t.Fatalf("stale mark not refreshed: %v", oldInfo.ModTime())
	}
	if !recentInfo.ModTime().Equal(f.now.Add(-10 * time.Minute)) {
		t.Fatalf("recent mark rewritten: %v", recentInfo.ModTime())
	}
}
