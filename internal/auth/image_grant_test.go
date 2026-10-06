package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type grantFixture struct {
	manager *Manager
	clock   *fixedClock
	session *Session
	handler http.Handler
	served  *string
}

func newGrantFixture(t *testing.T) *grantFixture {
	t.Helper()
	now := time.Date(2026, time.October, 6, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	manager := newDeterministicManager(t, clock, &deterministicTokenGenerator{
		ids:    []string{"sess-a", "sess-b"},
		tokens: []string{"token-a", "token-b"},
	})
	insertActiveUser(t, manager, "user-a", false, now)
	insertActiveUser(t, manager, "user-b", false, now)
	session, err := manager.CreateSession(t.Context(), "user-a", "test")
	if err != nil {
		t.Fatal(err)
	}
	served := ""
	handler := manager.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = UserFromContext(r.Context()).ID
		w.WriteHeader(http.StatusNoContent)
	}))
	return &grantFixture{manager: manager, clock: clock, session: session, handler: handler, served: &served}
}

func (f *grantFixture) do(method, target string) (int, string) {
	*f.served = ""
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec.Code, *f.served
}

func TestImageGrantAcceptedOnlyForItsMessagePathAndMethod(t *testing.T) {
	f := newGrantFixture(t)
	grant := f.manager.SignImageGrant(f.session, 7)
	if grant == "" {
		t.Fatal("no grant issued")
	}
	for _, target := range []string{"/api/inline-content/7/logo%40x?k=" + grant, "/api/remote-assets/7/abc.png?k=" + grant} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			if code, user := f.do(method, target); code != http.StatusNoContent || user != "user-a" {
				t.Errorf("%s %s = %d user %q, want served as user-a", method, target, code, user)
			}
		}
	}

	rejected := map[string]struct{ method, target string }{
		"other message id": {http.MethodGet, "/api/inline-content/8/logo?k=" + grant},
		"leading zero id":  {http.MethodGet, "/api/inline-content/07/logo?k=" + grant},
		"other route":      {http.MethodGet, "/api/messages/7?k=" + grant},
		"sibling route":    {http.MethodGet, "/api/inline-content-x/7/logo?k=" + grant},
		"POST":             {http.MethodPost, "/api/inline-content/7/logo?k=" + grant},
		"DELETE":           {http.MethodDelete, "/api/remote-assets/7/abc.png?k=" + grant},
		"tampered mac":     {http.MethodGet, "/api/inline-content/7/logo?k=" + grant[:len(grant)-2] + "AA"},
		"tampered expiry":  {http.MethodGet, "/api/inline-content/7/logo?k=sess-a.99999999999." + grant[len("sess-a.1234567890."):]},
		"tampered session": {http.MethodGet, "/api/inline-content/7/logo?k=sess-b" + grant[len("sess-a"):]},
		"garbage":          {http.MethodGet, "/api/inline-content/7/logo?k=not-a-grant"},
		"no grant":         {http.MethodGet, "/api/inline-content/7/logo"},
	}
	for name, tc := range rejected {
		if code, user := f.do(tc.method, tc.target); code != http.StatusUnauthorized || user != "" {
			t.Errorf("%s: %s %s = %d user %q, want 401", name, tc.method, tc.target, code, user)
		}
	}
}

func TestImageGrantExpires(t *testing.T) {
	f := newGrantFixture(t)
	grant := f.manager.SignImageGrant(f.session, 7)
	target := "/api/inline-content/7/logo?k=" + grant
	f.clock.now = f.clock.now.Add(imageGrantTTL - time.Second)
	if code, _ := f.do(http.MethodGet, target); code != http.StatusNoContent {
		t.Fatalf("grant just before expiry = %d, want 204", code)
	}
	f.clock.now = f.clock.now.Add(time.Second)
	if code, _ := f.do(http.MethodGet, target); code != http.StatusUnauthorized {
		t.Fatalf("expired grant = %d, want 401", code)
	}
}

func TestImageGrantForUserAIsNotValidAsUserB(t *testing.T) {
	f := newGrantFixture(t)
	sessionB, err := f.manager.CreateSession(t.Context(), "user-b", "test")
	if err != nil {
		t.Fatal(err)
	}
	grantA := f.manager.SignImageGrant(f.session, 7)
	grantB := f.manager.SignImageGrant(sessionB, 9)
	// Each grant runs as its own user only; the handler's ownership check then
	// decides, so A's grant can never act as B nor reach B's message id as B.
	if _, user := f.do(http.MethodGet, "/api/inline-content/7/x?k="+grantA); user != "user-a" {
		t.Errorf("grant A served as %q", user)
	}
	if _, user := f.do(http.MethodGet, "/api/inline-content/9/x?k="+grantB); user != "user-b" {
		t.Errorf("grant B served as %q", user)
	}
	if code, user := f.do(http.MethodGet, "/api/inline-content/9/x?k="+grantA); code != http.StatusUnauthorized || user != "" {
		t.Errorf("grant A on B's message id = %d %q, want 401", code, user)
	}
	// Splicing B's session id onto A's mac must not authenticate as B.
	spliced := "sess-b" + grantA[len("sess-a"):]
	if code, _ := f.do(http.MethodGet, "/api/inline-content/7/x?k="+spliced); code != http.StatusUnauthorized {
		t.Errorf("spliced grant = %d, want 401", code)
	}
}

func TestImageGrantDiesWithLogoutAndAuthVersionBump(t *testing.T) {
	f := newGrantFixture(t)
	grant := f.manager.SignImageGrant(f.session, 7)
	target := "/api/inline-content/7/logo?k=" + grant
	if code, _ := f.do(http.MethodGet, target); code != http.StatusNoContent {
		t.Fatalf("grant before = %d", code)
	}
	if _, err := f.manager.RevokeSessionByToken(t.Context(), f.session.Token, "user-a", SessionRevocationLogout); err != nil {
		t.Fatal(err)
	}
	if code, _ := f.do(http.MethodGet, target); code != http.StatusUnauthorized {
		t.Fatalf("grant after logout = %d, want 401", code)
	}

	f2 := newGrantFixture(t)
	grant2 := f2.manager.SignImageGrant(f2.session, 7)
	if _, err := f2.manager.db.Write().ExecContext(t.Context(), `UPDATE users SET auth_version = auth_version + 1 WHERE id = 'user-a'`); err != nil {
		t.Fatal(err)
	}
	if code, _ := f2.do(http.MethodGet, "/api/inline-content/7/logo?k="+grant2); code != http.StatusUnauthorized {
		t.Fatalf("grant after auth_version bump = %d, want 401", code)
	}
}

func TestImageGrantNotIssuedWithoutSessionOrKey(t *testing.T) {
	f := newGrantFixture(t)
	if g := f.manager.SignImageGrant(nil, 7); g != "" {
		t.Errorf("grant without session = %q", g)
	}
	keyless := NewManager(f.manager.config, f.manager.db, Dependencies{Clock: f.clock})
	if g := keyless.SignImageGrant(f.session, 7); g != "" {
		t.Errorf("grant without key = %q", g)
	}
}

func TestImageGrantQueryIgnoredInNoLoginMode(t *testing.T) {
	now := time.Date(2026, time.October, 6, 12, 0, 0, 0, time.UTC)
	manager := newDeterministicManager(t, &fixedClock{now: now}, &deterministicTokenGenerator{})
	manager.config.Enabled = false
	manager.config.Mode = ModeOpen
	var gotUser *User
	h := manager.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = UserFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, target := range []string{"/api/inline-content/7/logo", "/api/inline-content/7/logo?k=bogus"} {
		gotUser = nil
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusNoContent || gotUser == nil {
			t.Errorf("no-login %s = %d user %v, want default user", target, rec.Code, gotUser)
		}
	}
}
