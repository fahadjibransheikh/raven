package handler

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	avatarresolver "github.com/fahadjibransheikh/raven/internal/avatar"
	"github.com/fahadjibransheikh/raven/internal/mailauth"
	"github.com/fahadjibransheikh/raven/internal/storage"
	"github.com/fahadjibransheikh/raven/internal/store"
	"golang.org/x/oauth2"
)

const googleProfilePictureURL = "https://lh3.googleusercontent.com/a/owner-photo=s96-c"

type googleProfileFixture struct {
	h     *Handler
	db    *storage.DB
	blobs *store.BlobStore
	// ctx carries the stub for the userinfo request.
	ctx context.Context
	// userinfoStatus and photoStatus are what the stub Google servers answer.
	userinfoStatus atomic.Int32
	photoStatus    atomic.Int32
	userinfoCalls  atomic.Int32
	photoPaths     chan string
	// otherCalls counts requests to the ordinary providers, as (gravatar) hash lookups.
	gravatarCalls atomic.Int32
	gravatarImage atomic.Bool
}

func stubResponse(r *http.Request, status int, contentType, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

// newGoogleProfileFixture connects ownEmail as a Gmail mailbox and stands in for
// Google's userinfo and photo servers and for Gravatar.
func newGoogleProfileFixture(t *testing.T, ownEmail string) *googleProfileFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.New(filepath.Join(dir, "raven.db"))
	if err != nil {
		t.Fatalf("storage.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if _, err := db.Write().ExecContext(ctx, `INSERT OR IGNORE INTO users (id, username, username_normalized, name) VALUES ('default', 'default', 'default', 'Default')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO accounts (id, user_id, provider, provider_account_id, email_address) VALUES ('acc', 'default', 'gmail', 'google-subject', ?)`, ownEmail); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	manager := mailauth.New(&mailauth.Config{GoogleClient: &oauth2.Config{}}, db, testMailboxCredentialKey)
	expires := time.Now().Add(time.Hour)
	if err := manager.UpsertOAuthAccount(ctx, "acc", "google", "google-subject", "access-token", "refresh-token", "Bearer", &expires, "https://mail.google.com/"); err != nil {
		t.Fatalf("UpsertOAuthAccount() error = %v", err)
	}

	f := &googleProfileFixture{db: db, blobs: store.NewBlobStore(filepath.Join(dir, "blobs")), photoPaths: make(chan string, 8)}
	f.userinfoStatus.Store(http.StatusOK)
	f.photoStatus.Store(http.StatusOK)
	f.h = &Handler{db: db, blobStore: f.blobs, mailboxAuth: manager, avatar: avatarresolver.NewResolver(),
		avatarWarmupQueued: map[string]struct{}{}, avatarWarmupForced: map[string]time.Time{}}
	f.h.providerAvatarHTTPClient = &http.Client{Transport: calendarActionTransportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "lh3.googleusercontent.com" {
			t.Errorf("photo request to %s, want lh3.googleusercontent.com", r.URL.Host)
		}
		f.photoPaths <- r.URL.Path
		return stubResponse(r, int(f.photoStatus.Load()), "image/jpeg", "google-photo-bytes"), nil
	})}
	f.h.avatar.SetTransport(calendarActionTransportFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Host, "gravatar.com") && !strings.Contains(r.URL.Host, "libravatar.org") {
			t.Errorf("unexpected provider request to %s", r.URL.Host)
		}
		f.gravatarCalls.Add(1)
		if f.gravatarImage.Load() && strings.Contains(r.URL.Host, "gravatar.com") {
			return stubResponse(r, http.StatusOK, "image/png", "gravatar-bytes"), nil
		}
		return stubResponse(r, http.StatusNotFound, "text/plain", ""), nil
	}))
	userinfo := &http.Client{Transport: calendarActionTransportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "openidconnect.googleapis.com" || r.Header.Get("Authorization") != "Bearer access-token" {
			t.Errorf("userinfo request to %s with auth %q", r.URL.Host, r.Header.Get("Authorization"))
		}
		f.userinfoCalls.Add(1)
		return stubResponse(r, int(f.userinfoStatus.Load()), "application/json", `{"sub":"google-subject","email":"`+ownEmail+`","picture":"`+googleProfilePictureURL+`"}`), nil
	})}
	f.ctx = context.WithValue(ctx, oauth2.HTTPClient, userinfo)
	return f
}

func (f *googleProfileFixture) saveAvatar(t *testing.T, email, source string, expiresAt time.Time) string {
	t.Helper()
	hash := avatarresolver.GravatarHash(email)
	path, err := f.blobs.StoreAvatar(hash, "image/png", []byte(source+"-bytes"))
	if err != nil {
		t.Fatalf("StoreAvatar() error = %v", err)
	}
	if err := f.db.SaveSenderAvatarFound(f.ctx, hash, email, source, "image/png", path, nil, expiresAt, "unchecked", "unchecked"); err != nil {
		t.Fatalf("SaveSenderAvatarFound() error = %v", err)
	}
	return path
}

func outcomeStatuses(outcomes []avatarProviderOutcome) map[string]string {
	statuses := map[string]string{}
	for _, outcome := range outcomes {
		statuses[outcome.provider] = outcome.status
	}
	return statuses
}

func TestOwnGmailAddressUsesGoogleProfilePhotoFirst(t *testing.T) {
	f := newGoogleProfileFixture(t, "Owner@brand.example")
	email := "owner@brand.example"
	hash := avatarresolver.GravatarHash(email)
	oldPath := f.saveAvatar(t, email, "domain_icon", time.Now().Add(avatarFoundTTL))

	// A company domain with many senders would try bimi and domain_icon only.
	image, found, outcomes, err := f.h.fetchAndPersistAvatar(f.ctx, hash, email, nil, 50, nil)
	if err != nil || !found {
		t.Fatalf("fetchAndPersistAvatar() = (found %v, err %v), want google profile photo", found, err)
	}
	if got := avatarOutcomeSummary(outcomes); got != "google_profile:found" || image.Source != avatarGoogleProfile {
		t.Fatalf("outcomes = %s, source = %q, want only google_profile:found", got, image.Source)
	}
	if path := <-f.photoPaths; path != "/a/owner-photo=s256-c" {
		t.Fatalf("photo requested at %q, want the 256 pixel version", path)
	}
	rec, err := f.db.GetSenderAvatarByHash(f.ctx, hash)
	if err != nil || rec == nil {
		t.Fatalf("GetSenderAvatarByHash() = (%+v, %v)", rec, err)
	}
	if rec.Status != "found" || rec.Source != avatarGoogleProfile || rec.StoragePath == oldPath {
		t.Fatalf("record = %+v, want the domain icon replaced by a google_profile photo", rec)
	}
	data, err := f.blobs.ReadAvatar(rec.StoragePath)
	if err != nil || string(data) != "google-photo-bytes" {
		t.Fatalf("stored photo = (%q, %v), want the downloaded bytes", data, err)
	}
	logs, err := f.db.GetRecentSenderAvatarAttemptLogs(f.ctx, 10)
	if err != nil || len(logs) != 1 || logs[0].Provider != avatarGoogleProfile || logs[0].Status != "found" {
		t.Fatalf("attempt logs = (%+v, %v), want one google_profile found entry", logs, err)
	}
}

func TestOwnGmailAddressFallsBackToOrdinaryProvidersWhenGoogleFails(t *testing.T) {
	for name, setup := range map[string]func(*googleProfileFixture){
		"userinfo fails": func(f *googleProfileFixture) { f.userinfoStatus.Store(http.StatusInternalServerError) },
		"photo missing":  func(f *googleProfileFixture) { f.photoStatus.Store(http.StatusNotFound) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newGoogleProfileFixture(t, "owner@gmail.com")
			setup(f)
			f.gravatarImage.Store(true)
			email := "owner@gmail.com"
			hash := avatarresolver.GravatarHash(email)

			image, found, outcomes, err := f.h.fetchAndPersistAvatar(f.ctx, hash, email, nil, 0, nil)
			if err != nil || !found || image.Source != "gravatar" {
				t.Fatalf("fetchAndPersistAvatar() = (%q, found %v, err %v), want the gravatar fallback", image.Source, found, err)
			}
			statuses := outcomeStatuses(outcomes)
			if statuses["gravatar"] != "found" || statuses[avatarGoogleProfile] == "found" || statuses[avatarGoogleProfile] == "" {
				t.Fatalf("outcomes = %s, want a failed google_profile attempt then gravatar", avatarOutcomeSummary(outcomes))
			}
			if outcomes[0].provider != avatarGoogleProfile {
				t.Fatalf("outcomes = %s, want google_profile tried first", avatarOutcomeSummary(outcomes))
			}
		})
	}
}

func TestOwnGmailAddressKeepsGoogleProfilePhotoWhenRenewalFails(t *testing.T) {
	f := newGoogleProfileFixture(t, "owner@brand.example")
	f.userinfoStatus.Store(http.StatusServiceUnavailable)
	email := "owner@brand.example"
	hash := avatarresolver.GravatarHash(email)
	path := f.saveAvatar(t, email, avatarGoogleProfile, time.Now().Add(-time.Minute))

	// The stub for the ordinary providers fails the test on any request, and
	// a company domain's site icon would otherwise be looked up.
	_, found, outcomes, err := f.h.fetchAndPersistAvatar(f.ctx, hash, email, nil, 50, nil)
	if err != nil || !found {
		t.Fatalf("fetchAndPersistAvatar() = (found %v, err %v), want the saved photo kept", found, err)
	}
	if got := avatarOutcomeSummary(outcomes); got != "google_profile:error" {
		t.Fatalf("outcomes = %s, want only google_profile:error", got)
	}
	rec, err := f.db.GetSenderAvatarByHash(f.ctx, hash)
	if err != nil || rec == nil || rec.Source != avatarGoogleProfile || rec.StoragePath != path || rec.Status != "found" {
		t.Fatalf("record = (%+v, %v), want the google_profile photo untouched", rec, err)
	}
	if !rec.ExpiresAtValid || !rec.ExpiresAt.After(time.Now().Add(avatarGoogleProfileRetryAfter-time.Minute)) {
		t.Fatalf("expires at %v, want about %v from now", rec.ExpiresAt, avatarGoogleProfileRetryAfter)
	}
}

func TestOwnGmailPhotoGivesWayWhenGoogleHasNoPhoto(t *testing.T) {
	f := newGoogleProfileFixture(t, "owner@gmail.com")
	f.photoStatus.Store(http.StatusNotFound)
	f.gravatarImage.Store(true)
	email := "owner@gmail.com"
	hash := avatarresolver.GravatarHash(email)
	f.saveAvatar(t, email, avatarGoogleProfile, time.Now().Add(-time.Minute))

	image, found, _, err := f.h.fetchAndPersistAvatar(f.ctx, hash, email, nil, 0, nil)
	if err != nil || !found || image.Source != "gravatar" {
		t.Fatalf("fetchAndPersistAvatar() = (%q, found %v, err %v), want the photo removed at Google replaced", image.Source, found, err)
	}
}

func TestOtherSendersNeverGetTheOwnersGoogleProfilePhoto(t *testing.T) {
	f := newGoogleProfileFixture(t, "owner@brand.example")
	f.saveAvatar(t, "owner@brand.example", avatarGoogleProfile, time.Now().Add(time.Hour))

	other := "bob@brand.example"
	if id, err := f.h.ownGmailAccountID(f.ctx, other); err != nil || id != "" {
		t.Fatalf("ownGmailAccountID(%q) = (%q, %v), want no account, so no Google lookup", other, id, err)
	}
	if f.userinfoCalls.Load() != 0 {
		t.Fatalf("userinfo calls = %d, want 0", f.userinfoCalls.Load())
	}
	image, _, found, err := f.h.reuseStoredDomainIconAvatar(f.ctx, avatarresolver.GravatarHash(other), other, "unchecked", "unchecked")
	if err != nil || found {
		t.Fatalf("reuseStoredDomainIconAvatar() = (%+v, found %v, err %v), want the owner's photo not reused", image, found, err)
	}
	if rec, err := f.db.GetSenderAvatarByHash(f.ctx, avatarresolver.GravatarHash(other)); err != nil || rec != nil {
		t.Fatalf("record for %s = (%+v, %v), want none", other, rec, err)
	}
}

func TestBackfillReplacesOwnGmailIconOnceAndRetriesFailuresLater(t *testing.T) {
	f := newGoogleProfileFixture(t, "owner@brand.example")
	email := "owner@brand.example"
	hash := avatarresolver.GravatarHash(email)
	f.saveAvatar(t, email, "domain_icon", time.Now().Add(avatarFoundTTL))

	f.h.runAvatarBackfill(f.ctx, 1, false, time.Now(), "scheduled")
	rec, err := f.db.GetSenderAvatarByHash(f.ctx, hash)
	if err != nil || rec == nil || rec.Source != avatarGoogleProfile {
		t.Fatalf("record after backfill = (%+v, %v), want the google_profile photo", rec, err)
	}
	f.h.runAvatarBackfill(f.ctx, 2, false, time.Now(), "scheduled")
	if got := f.userinfoCalls.Load(); got != 1 {
		t.Fatalf("userinfo calls after a second backfill = %d, want 1", got)
	}

	// A mailbox whose lookup failed is not looked up again until the retry wait passes.
	g := newGoogleProfileFixture(t, "owner@gmail.com")
	g.userinfoStatus.Store(http.StatusInternalServerError)
	g.gravatarImage.Store(true)
	g.saveAvatar(t, "owner@gmail.com", "libravatar", time.Now().Add(avatarFoundTTL))
	g.h.runAvatarBackfill(g.ctx, 1, false, time.Now(), "scheduled")
	g.h.runAvatarBackfill(g.ctx, 2, false, time.Now(), "scheduled")
	if got := g.userinfoCalls.Load(); got != 1 {
		t.Fatalf("userinfo calls after two backfills with a failing lookup = %d, want 1", got)
	}
	rec, err = g.db.GetSenderAvatarByHash(g.ctx, avatarresolver.GravatarHash("owner@gmail.com"))
	if err != nil || rec == nil || rec.Source != "gravatar" {
		t.Fatalf("record after failed lookup = (%+v, %v), want the ordinary fallback", rec, err)
	}
}

func TestGoogleProfilePhotoSizeRewrite(t *testing.T) {
	tests := map[string]string{
		"https://lh3.googleusercontent.com/a/x=s96-c":   "https://lh3.googleusercontent.com/a/x=s256-c",
		"https://lh3.googleusercontent.com/a/x=s64":     "https://lh3.googleusercontent.com/a/x=s256-c",
		"https://lh3.googleusercontent.com/a/photo.jpg": "https://lh3.googleusercontent.com/a/photo.jpg",
	}
	for in, want := range tests {
		if got := googleProfilePhotoSize.ReplaceAllString(in, "=s256-c"); got != want {
			t.Errorf("rewrite(%q) = %q, want %q", in, got, want)
		}
	}
}
