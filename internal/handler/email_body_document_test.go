package handler

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/storage"
)

const injectedColor = `rgb(</style><script>alert(document.domain)</script><x)`

func TestSafeEmailCSSColorAcceptsOnlyPlainColors(t *testing.T) {
	for _, value := range []string{"#f8f2e6", "#ABC", "rgb(42, 37, 32)", "rgba(42,37,32,0.5)", "rgba(42, 37, 32, .5)", " #2a2520 "} {
		if got := safeEmailCSSColor(value, "fallback"); got != strings.TrimSpace(value) {
			t.Errorf("safeEmailCSSColor(%q) = %q, want it kept", value, got)
		}
	}
	for _, value := range []string{
		injectedColor,
		"rgb(1,2,3);color:red",
		"rgb(1,2,3)}body{x:y",
		"rgb(1,2,3) </style>",
		`rgb(1,2,3)"`,
		"rgb(var(--x),2,3)",
		"rgb(1,2)",
		"#abcd",
		"#ggg",
		"red",
		"url(https://example.test/x)",
	} {
		if got := safeEmailCSSColor(value, "fallback"); got != "fallback" {
			t.Errorf("safeEmailCSSColor(%q) = %q, want fallback", value, got)
		}
	}
}

// The reader's colors arrive in the body URL, so a crafted link could carry
// markup into the document. They must be dropped, and the document must stay
// sandboxed even when the link is opened outside the reader's iframe.
func TestOwnedEmailBodyIgnoresInjectedColorsAndStaysSandboxed(t *testing.T) {
	f := newOwnedMessageContentFixture(t)
	query := url.Values{"theme": {"light"}, "bg": {injectedColor}, "fg": {injectedColor}, "link": {injectedColor}}
	rec := httptest.NewRecorder()
	f.h.handleUserEmailBody(rec, ownedContentRequest("alice", "/email/1/body?"+query.Encode(), ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, "alert(document.domain)") || !strings.Contains(body, "alice private body") {
		t.Fatalf("body carries the injected color or lost the message: %q", body)
	}
	assertEmailBodyDocumentSandboxed(t, rec)
}

func TestEmailBodyErrorDocumentStaysSandboxed(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Handler{}).writeEmailBodyError(rec, context.Background(), "owner", "42", "", sql.ErrNoRows)
	assertEmailBodyDocumentSandboxed(t, rec)
}

func assertEmailBodyDocumentSandboxed(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	policy := rec.Header().Get("Content-Security-Policy")
	// Scripts only: no popups (the reader's page opens links) and no same-origin.
	if !strings.HasPrefix(policy, "sandbox allow-scripts;") || strings.Contains(policy, "allow-same-origin") || strings.Contains(policy, "allow-popups") {
		t.Fatalf("Content-Security-Policy = %q, want the reader's sandbox with scripts only", policy)
	}
	if !strings.Contains(policy, "frame-ancestors 'self'") {
		t.Fatalf("Content-Security-Policy = %q, lost frame-ancestors", policy)
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("headers = %v", rec.Header())
	}
}

var scriptTag = regexp.MustCompile(`<script[^>]*>`)

// Only the reader's own scripts may run in a body document: each carries the
// response's nonce, and the policy names that nonce and nothing else.
func assertOnlyNoncedScripts(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	policy := rec.Header().Get("Content-Security-Policy")
	m := regexp.MustCompile(`script-src 'nonce-([A-Za-z0-9]+)'`).FindStringSubmatch(policy)
	if m == nil || len(m[1]) < 16 {
		t.Fatalf("Content-Security-Policy = %q, want a script nonce", policy)
	}
	for _, directive := range []string{"default-src 'none'", "form-action 'none'", "base-uri 'none'"} {
		if !strings.Contains(policy, directive) {
			t.Fatalf("Content-Security-Policy = %q, missing %q", policy, directive)
		}
	}
	tags := scriptTag.FindAllString(rec.Body.String(), -1)
	if len(tags) == 0 {
		t.Fatal("body document has none of the reader's scripts")
	}
	for _, tag := range tags {
		if tag != `<script nonce="`+m[1]+`">` {
			t.Fatalf("script %q does not carry the response nonce %q", tag, m[1])
		}
	}
	return policy
}

func TestOwnedEmailBodyRunsOnlyReaderScriptsAndHoldsRemoteContent(t *testing.T) {
	f := newOwnedMessageContentFixture(t)
	body := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		f.h.handleUserEmailBody(rec, ownedContentRequest("alice", "/email/1/body?theme=dark", ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
		}
		return rec
	}
	first := body()
	policy := assertOnlyNoncedScripts(t, first)
	if strings.Contains(policy, "https:") || !strings.Contains(policy, "img-src 'self' data:;") {
		t.Fatalf("Content-Security-Policy = %q, want remote content held back", policy)
	}
	if second := body(); second.Header().Get("Content-Security-Policy") == policy {
		t.Fatal("nonce was reused across responses")
	}
	if err := f.h.withUserDB(t.Context(), "alice", func(db *storage.DB) error {
		return db.AllowRemoteContentForMessageForUser(t.Context(), 1, "alice")
	}); err != nil {
		t.Fatal(err)
	}
	// Allowed remote content comes through Gofer, so the policy stays closed.
	allowed := body()
	if policy := assertOnlyNoncedScripts(t, allowed); strings.Contains(policy, "http") {
		t.Fatalf("Content-Security-Policy = %q, want no direct web loads", policy)
	}
	if page := allowed.Body.String(); strings.Contains(page, `src="https://`) || !strings.Contains(page, `src="/api/message-file/web/1/`) {
		t.Fatalf("allowed remote image was not relayed: %s", page)
	}
}

func TestEmailBodyErrorScriptCarriesNonce(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Handler{}).writeEmailBodyError(rec, context.Background(), "owner", "42", "", sql.ErrNoRows)
	assertOnlyNoncedScripts(t, rec)
}
