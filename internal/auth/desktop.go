package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The desktop gate protects no-login (open mode) installs that the Tauri
// wrapper runs on loopback. Without it any local process can read and send mail
// with `curl -H 'X-Gofer-Request: 1' 127.0.0.1:8090`. The wrapper generates a
// random token per launch, hands it to the sidecar in GOFER_DESKTOP_TOKEN and
// navigates its webview to /desktop-auth?t=<token>, which trades the token for
// an HttpOnly cookie. It is inert unless the variable is set and the instance
// is in open mode, so web/server deployments are unaffected.
const (
	DesktopAuthPath    = "/desktop-auth"
	desktopCookieName  = "raven_desktop"
	desktopMarkerKey   = "X-Raven-Desktop"
	desktopGrantPrefix = "d."
	desktopGrantLabel  = "gofer-desktop-image-grant-v1"
)

// minimumDesktopTokenBytes rejects a guessable token rather than running with it.
const minimumDesktopTokenBytes = 32

// ValidateDesktopToken fails closed: a token that is set but unusable must stop
// startup, never silently leave the instance open to local processes.
func (c *Config) ValidateDesktopToken() error {
	if c.DesktopToken == "" {
		return nil
	}
	if c.Enabled {
		return errors.New("GOFER_DESKTOP_TOKEN only applies to open (no-login) mode")
	}
	if len(c.DesktopToken) < minimumDesktopTokenBytes {
		return fmt.Errorf("GOFER_DESKTOP_TOKEN must be at least %d characters", minimumDesktopTokenBytes)
	}
	return nil
}

func (m *Manager) desktopGateActive() bool {
	return m != nil && !m.config.Enabled && len(m.config.DesktopToken) >= minimumDesktopTokenBytes
}

func (m *Manager) desktopTokenMatches(candidate string) bool {
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(m.config.DesktopToken)) == 1
}

// desktopSafeNext only allows a same-origin absolute path, never a scheme-relative
// or backslash form a browser could resolve to another host.
func desktopSafeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n") {
		return "/"
	}
	return next
}

// desktopGate reports whether it fully handled the request. Requests that carry
// the desktop cookie fall through to the normal open-mode flow.
func (m *Manager) desktopGate(w http.ResponseWriter, r *http.Request) bool {
	// Lets the wrapper tell this server from any other process on the port
	// before it sends the token anywhere.
	w.Header().Set(desktopMarkerKey, "1")

	if r.URL.Path == DesktopAuthPath {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method != http.MethodGet || !m.desktopTokenMatches(r.URL.Query().Get("t")) {
			http.Error(w, "desktop session required", http.StatusForbidden)
			return true
		}
		// Lax rather than Strict: after a mailbox OAuth round trip the provider
		// redirects back with a cross-site top-level navigation, and Strict
		// would drop the cookie on exactly that request.
		http.SetCookie(w, &http.Cookie{
			Name:     desktopCookieName,
			Value:    m.config.DesktopToken,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, desktopSafeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
		return true
	}

	if cookie, err := r.Cookie(desktopCookieName); err == nil && m.desktopTokenMatches(cookie.Value) {
		return false
	}
	// The sandboxed email iframe is cookieless; a per-message grant stands in on
	// the two image routes only.
	if m.desktopImageGrantValid(r) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "desktop session required", http.StatusForbidden)
	return true
}

func (m *Manager) desktopImageGrantMAC(messageID, expires int64) []byte {
	mac := hmac.New(sha256.New, []byte(m.config.DesktopToken))
	for _, part := range []string{desktopGrantLabel, strconv.FormatInt(messageID, 10), strconv.FormatInt(expires, 10)} {
		_, _ = mac.Write([]byte(strconv.Itoa(len(part)) + ":" + part))
	}
	return mac.Sum(nil)
}

// signDesktopImageGrant is the no-session counterpart of SignImageGrant: open
// mode has no session to bind to, so the grant is keyed by the launch token and
// dies with the process.
func (m *Manager) signDesktopImageGrant(messageID int64) string {
	if !m.desktopGateActive() || messageID <= 0 {
		return ""
	}
	expires := m.clock.Now().Add(imageGrantTTL).Unix()
	return desktopGrantPrefix + strconv.FormatInt(expires, 10) + "." + base64.RawURLEncoding.EncodeToString(m.desktopImageGrantMAC(messageID, expires))
}

func (m *Manager) desktopImageGrantValid(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	messageID, ok := imageGrantMessageID(r.URL.Path)
	grant, hasPrefix := strings.CutPrefix(r.URL.Query().Get(ImageGrantQuery), desktopGrantPrefix)
	if !ok || !hasPrefix {
		return false
	}
	expiresRaw, macRaw, found := strings.Cut(grant, ".")
	if !found {
		return false
	}
	expires, err := strconv.ParseInt(expiresRaw, 10, 64)
	now := m.clock.Now().Unix()
	if err != nil || now >= expires || expires-now > int64(imageGrantTTL/time.Second) {
		return false
	}
	presented, err := base64.RawURLEncoding.DecodeString(macRaw)
	return err == nil && hmac.Equal(presented, m.desktopImageGrantMAC(messageID, expires))
}
