package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionTokenFallsBackToLegacyCookie(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "gofer_session", Value: "old-token"})
	if got := GetSessionToken(r); got != "old-token" {
		t.Fatalf("token = %q; want the legacy cookie's value", got)
	}
}

func TestSessionTokenPrefersNewCookie(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "gofer_session", Value: "old-token"})
	r.AddCookie(&http.Cookie{Name: "raven_session", Value: "new-token"})
	if got := GetSessionToken(r); got != "new-token" {
		t.Fatalf("token = %q; want the new cookie's value", got)
	}
}

func TestSessionCookieIsWrittenUnderNewNameAndLegacyIsExpired(t *testing.T) {
	rec := httptest.NewRecorder()
	SetSessionCookie(rec, "token", true)
	cookies := map[string]*http.Cookie{}
	for _, c := range rec.Result().Cookies() {
		cookies[c.Name] = c
	}
	fresh, legacy := cookies["raven_session"], cookies["gofer_session"]
	if fresh == nil || fresh.Value != "token" || fresh.MaxAge <= 0 || !fresh.HttpOnly || !fresh.Secure {
		t.Fatalf("new cookie = %+v", fresh)
	}
	if legacy == nil || legacy.Value != "" || legacy.MaxAge >= 0 || legacy.Path != "/" || !legacy.Secure {
		t.Fatalf("legacy cookie should be expired with the same scope, got %+v", legacy)
	}
}

func TestClearSessionCookieClearsBothNames(t *testing.T) {
	rec := httptest.NewRecorder()
	ClearSessionCookie(rec, false)
	cleared := map[string]bool{}
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			cleared[c.Name] = true
		}
	}
	if !cleared["raven_session"] || !cleared["gofer_session"] {
		t.Fatalf("cleared = %v", cleared)
	}
}

func TestOtherAuthCookiesFallBackToLegacyNames(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "gofer_pre_auth", Value: "pre"})
	if got := GetPreAuthToken(r); got != "pre" {
		t.Fatalf("pre-auth token = %q", got)
	}
	// "/settings" base64url-encoded
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "gofer_auth_return_to", Value: "L3NldHRpbmdz"})
	if got := GetReturnTo(r); got != "/settings" {
		t.Fatalf("return-to = %q", got)
	}
}

// currentCookies returns the cookies a response sets under their current names,
// leaving out the expiry headers for the legacy gofer_* twins.
func currentCookies(response *http.Response) []*http.Cookie {
	var current []*http.Cookie
	for _, cookie := range response.Cookies() {
		if !strings.HasPrefix(cookie.Name, legacyCookiePrefix) {
			current = append(current, cookie)
		}
	}
	return current
}
