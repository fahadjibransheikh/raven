package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ImageGrantQuery is the query parameter that carries a grant.
const ImageGrantQuery = "k"

// imageGrantTTL: an email body is rendered once per open and its images load
// immediately, so the grant only has to outlive that render plus iframe reloads
// and lazy loads in a message left open. Long enough for that, short enough
// that a leaked URL (history, logs) stops working quickly. Each body render
// mints a fresh grant.
const imageGrantTTL = time.Hour

const imageGrantContext = "gofer-image-grant-v1"

var imageGrantPrefixes = [...]string{"/api/inline-content/", "/api/remote-assets/"}

// imageGrantMessageID extracts the message id segment from an image route path.
func imageGrantMessageID(path string) (int64, bool) {
	for _, prefix := range imageGrantPrefixes {
		rest, ok := strings.CutPrefix(path, prefix)
		if !ok {
			continue
		}
		segment, _, _ := strings.Cut(rest, "/")
		id, err := strconv.ParseInt(segment, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != segment {
			return 0, false
		}
		return id, true
	}
	return 0, false
}

func (m *Manager) imageGrantMAC(session *Session, messageID, expires int64) []byte {
	mac := hmac.New(sha256.New, m.bucketHashKey)
	for _, part := range []string{imageGrantContext, session.ID, session.UserID, strconv.FormatInt(session.AuthVersion, 10), strconv.FormatInt(messageID, 10), strconv.FormatInt(expires, 10)} {
		// Length-prefix so field boundaries cannot be shifted.
		_, _ = mac.Write([]byte(strconv.Itoa(len(part)) + ":" + part))
	}
	return mac.Sum(nil)
}

// SignImageGrant returns a short-lived, read-only grant letting the sandboxed
// (opaque-origin, cookieless) email iframe fetch one message's inline and
// remote images. It is bound to the live session, so logout, a password change
// or any auth_version bump revokes it. Empty when there is no session (no-login
// mode needs none) or no server key.
func (m *Manager) SignImageGrant(session *Session, messageID int64) string {
	if m == nil || session == nil || messageID <= 0 || len(m.bucketHashKey) < minimumBucketHashKeyBytes {
		return ""
	}
	expires := m.clock.Now().Add(imageGrantTTL).Unix()
	return session.ID + "." + strconv.FormatInt(expires, 10) + "." + base64.RawURLEncoding.EncodeToString(m.imageGrantMAC(session, messageID, expires))
}

// userForImageGrant authenticates a request by grant alone. It only ever
// accepts GET/HEAD on the two image routes, for the message named in the path.
// Any failure returns nil and the caller falls through to cookie auth.
func (m *Manager) userForImageGrant(r *http.Request) *User {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return nil
	}
	grant := r.URL.Query().Get(ImageGrantQuery)
	messageID, ok := imageGrantMessageID(r.URL.Path)
	if grant == "" || !ok || len(m.bucketHashKey) < minimumBucketHashKeyBytes {
		return nil
	}
	parts := strings.Split(grant, ".")
	if len(parts) != 3 {
		return nil
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || m.clock.Now().Unix() >= expires || expires-m.clock.Now().Unix() > int64(imageGrantTTL/time.Second) {
		return nil
	}
	presented, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil
	}
	session, err := m.activeSession(r.Context(), "id", parts[0], false)
	if err != nil || session == nil || session.PasswordChangeRequired {
		return nil
	}
	if !hmac.Equal(presented, m.imageGrantMAC(session, messageID, expires)) {
		return nil
	}
	user, err := m.GetUserByID(r.Context(), session.UserID)
	if err != nil || user == nil || !user.Status.AllowsAuthentication() || user.AuthVersion != session.AuthVersion || user.IsManagement() {
		return nil
	}
	return user
}
