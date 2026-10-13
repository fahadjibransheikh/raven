package auth

import (
	"net/http"
	"strings"
	"time"
)

// Cookies written before the rename were called gofer_*. Browsers still hold
// them, so reads fall back to the old name (nobody is signed out), writes use
// the new name only, and every write also expires the old one so it cannot
// outlive a logout.
const (
	cookiePrefix       = "raven_"
	legacyCookiePrefix = "gofer_"
)

func legacyCookieName(name string) string {
	return legacyCookiePrefix + strings.TrimPrefix(name, cookiePrefix)
}

// readCookie returns the named cookie, or the one it replaced.
func readCookie(r *http.Request, name string) (*http.Cookie, error) {
	if cookie, err := r.Cookie(name); err == nil {
		return cookie, nil
	}
	return r.Cookie(legacyCookieName(name))
}

// setCookie sends cookie and expires its legacy twin with the same scope.
func setCookie(w http.ResponseWriter, cookie *http.Cookie) {
	http.SetCookie(w, cookie)
	legacy := *cookie
	legacy.Name = legacyCookieName(cookie.Name)
	legacy.Value = ""
	legacy.MaxAge = -1
	legacy.Expires = time.Time{}
	http.SetCookie(w, &legacy)
}
