package handler

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	avatarresolver "github.com/fahadjibransheikh/raven/internal/avatar"
	"github.com/fahadjibransheikh/raven/internal/storage"
)

func TestAvatarRetryWaitDoublesAfterEachFailedLookup(t *testing.T) {
	day := 24 * time.Hour
	for _, c := range []struct{ base, last, want time.Duration }{
		{day, 0, day},             // first failure, or the last lookup found a picture
		{day, day, 2 * day},       // doubles
		{day, 8 * day, 16 * day},  // keeps doubling
		{day, 20 * day, 30 * day}, // up to the most
		{day, 60 * day, 30 * day}, // even after a longer wait
		{6 * time.Hour, 0, 6 * time.Hour},
		{6 * time.Hour, 12 * time.Hour, day},
		{6 * time.Hour, time.Hour, 6 * time.Hour}, // never less than the base
	} {
		if got := avatarRetryWait(c.base, c.last); got != c.want {
			t.Errorf("wait after %v (base %v) = %v, want %v", c.last, c.base, got, c.want)
		}
	}
}

func TestAvatarDomainMissingRecognizesUnknownDomains(t *testing.T) {
	notFound := &net.DNSError{Err: "no such host", Name: "vibehive.info", IsNotFound: true}
	if !avatarDomainMissing(notFound) || !avatarDomainMissing(&url.Error{Op: "Get", URL: "https://vibehive.info/", Err: notFound}) || !avatarDomainMissing(fmt.Errorf("icon: %w", notFound)) {
		t.Fatal("an unknown domain was not recognized")
	}
	for _, err := range []error{
		&net.DNSError{Err: "server misbehaving", Name: "example.com", IsTemporary: true},
		errors.New("domain_icon returned 429"),
		errors.New("lookup example.com: no such host"), // only the typed error counts
	} {
		if avatarDomainMissing(err) {
			t.Fatalf("%v counted as an unknown domain", err)
		}
	}
}

// A sender on screen gets an early retry after an error, but not once its
// lookups have kept failing.
func TestAvatarEarlyRetryStopsAfterRepeatedFailures(t *testing.T) {
	ctx := t.Context()
	db, err := storage.New(filepath.Join(t.TempDir(), "raven.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := &Handler{db: db, avatarWarmupForced: map[string]time.Time{}}
	email := "news@brand.example"
	hash := avatarresolver.GravatarHash(email)
	if err := db.SaveSenderAvatarError(ctx, hash, email, "domain_icon", "refused", time.Now().Add(6*time.Hour), "missing", "missing"); err != nil {
		t.Fatal(err)
	}
	if _, ok, forced, _, err := h.avatarWarmupCandidate(ctx, email, hash); err != nil || !ok || !forced {
		t.Fatalf("first error: candidate=%v forced=%v err=%v, want an early retry", ok, forced, err)
	}
	// After a few errors the wait has grown to a day or more.
	if err := db.SaveSenderAvatarError(ctx, hash, email, "domain_icon", "refused", time.Now().Add(avatarForcedRetryMaxWait), "missing", "missing"); err != nil {
		t.Fatal(err)
	}
	delete(h.avatarWarmupForced, hash)
	if _, ok, _, _, err := h.avatarWarmupCandidate(ctx, email, hash); err != nil || ok {
		t.Fatalf("after repeated errors: candidate=%v err=%v, want no early retry", ok, err)
	}
}
