package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/auth"
	"github.com/cristianadrielbraun/gofer/internal/mail"
)

func TestSSEEventVisibleRequiresAnOwnedAudience(t *testing.T) {
	accounts := map[string]bool{"owned": true}
	tests := []struct {
		name    string
		event   mail.Event
		userID  string
		isAdmin bool
		want    bool
	}{
		{name: "owned account", event: mail.Event{AccountID: "owned"}, userID: "user-a", want: true},
		{name: "foreign account", event: mail.Event{AccountID: "foreign"}, userID: "user-a", want: false},
		{name: "owned user", event: mail.Event{UserID: "user-a"}, userID: "user-a", want: true},
		{name: "foreign user", event: mail.Event{UserID: "user-b"}, userID: "user-a", want: false},
		{name: "owned user list", event: mail.Event{UserIDs: []string{"user-b", "user-a"}}, userID: "user-a", want: true},
		{name: "foreign user list", event: mail.Event{UserIDs: []string{"user-b"}}, userID: "user-a", want: false},
		{name: "legacy payload owner", event: mail.Event{Payload: map[string]any{"user_id": "user-a"}}, userID: "user-a", want: true},
		{name: "legacy payload foreign", event: mail.Event{Payload: map[string]any{"user_id": "user-b"}}, userID: "user-a", want: false},
		{name: "admin only admin", event: mail.Event{AdminOnly: true}, userID: "admin", isAdmin: true, want: true},
		{name: "admin only regular user", event: mail.Event{AdminOnly: true}, userID: "user-a", want: false},
		{name: "unscoped", event: mail.Event{}, userID: "user-a", want: false},
		{name: "admin does not bypass account ownership", event: mail.Event{AccountID: "foreign"}, userID: "admin", isAdmin: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sseEventVisible(tt.event, tt.userID, accounts, tt.isAdmin); got != tt.want {
				t.Fatalf("sseEventVisible() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGlobalProcessingStatusRequiresAdmin(t *testing.T) {
	h := &Handler{}
	next := h.adminOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tt := range []struct {
		name string
		user *auth.User
		want int
	}{
		{name: "unauthenticated", user: nil, want: http.StatusForbidden},
		{name: "regular user", user: &auth.User{ID: "user-a"}, want: http.StatusForbidden},
		{name: "administrator", user: &auth.User{ID: "admin", IsAdmin: true}, want: http.StatusNoContent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/system/processing", nil)
			if tt.user != nil {
				req = req.WithContext(auth.ContextWithUser(req.Context(), tt.user))
			}
			recorder := httptest.NewRecorder()
			next.ServeHTTP(recorder, req)
			if recorder.Code != tt.want {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.want)
			}
		})
	}
}

func TestSSEStreamEndsWhenItsSessionIsRevoked(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	h.syncer = mail.NewSyncOrchestrator(db, nil, nil, nil)
	h.auth = auth.NewManager(&auth.Config{Enabled: true, BaseURL: "https://gofer.example"}, db, auth.Dependencies{BucketHashKey: []byte("0123456789abcdef0123456789abcdef")})
	session, err := h.auth.CreateAuthenticatedSession(t.Context(), "owner", "test", auth.AuthenticationMethodPassword, auth.AssuranceLevelSingleFactor)
	if err != nil {
		t.Fatal(err)
	}
	old := sseRecheckInterval
	sseRecheckInterval = 20 * time.Millisecond
	t.Cleanup(func() { sseRecheckInterval = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.handleSSE(w, ownerRequest(r))
	}))
	defer srv.Close()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	req.AddCookie(&http.Cookie{Name: "gofer_session", Value: session.Token})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	ended := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, resp.Body); close(ended) }()
	select {
	case <-ended:
		t.Fatal("stream ended while the session was still valid")
	case <-time.After(150 * time.Millisecond):
	}
	if _, err := db.Write().Exec(`UPDATE sessions SET revoked_at = CURRENT_TIMESTAMP, revocation_reason = 'logout' WHERE user_id = 'owner'`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("stream stayed open after its session was revoked")
	}
}
