package httpguard

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPagesRunOnlyGoferScripts(t *testing.T) {
	for _, directive := range []string{"script-src 'self'", "object-src 'none'", "base-uri 'self'", "frame-ancestors 'self'"} {
		if !strings.Contains(PagePolicy, directive) {
			t.Fatalf("page policy %q lacks %q", PagePolicy, directive)
		}
	}
	for _, unsafe := range []string{"unsafe-inline", "unsafe-eval", "unsafe-hashes", "*"} {
		if strings.Contains(PagePolicy, unsafe) {
			t.Fatalf("page policy %q allows %q", PagePolicy, unsafe)
		}
	}
}

func TestScriptPolicyReportsAreLoggedBriefly(t *testing.T) {
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(previous) })
	scriptPolicyReports.Lock()
	scriptPolicyReports.count = 0
	scriptPolicyReports.Unlock()

	body := `{"csp-report":{"document-uri":"https://gofer.example/settings","blocked-uri":"inline","effective-directive":"script-src-attr","source-file":"https://gofer.example/settings","line-number":12,"script-sample":"wizardGo(2)\nnext line"}}`
	rec := httptest.NewRecorder()
	handleScriptPolicyReport(rec, httptest.NewRequest(http.MethodPost, ScriptPolicyReportPath, strings.NewReader(body)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("report = %d", rec.Code)
	}
	line := logged.String()
	if !strings.Contains(line, "script-src-attr blocked inline on https://gofer.example/settings") || strings.Count(strings.TrimSpace(line), "\n") != 0 {
		t.Fatalf("logged %q", line)
	}

	// A flood of reports is cut off.
	logged.Reset()
	for range 3 * scriptPolicyReportsPerMinute {
		handleScriptPolicyReport(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, ScriptPolicyReportPath, strings.NewReader(body)))
	}
	if n := strings.Count(logged.String(), "script policy:"); n >= scriptPolicyReportsPerMinute {
		t.Fatalf("logged %d reports in a minute", n)
	}
	if rec := httptest.NewRecorder(); func() int {
		handleScriptPolicyReport(rec, httptest.NewRequest(http.MethodGet, ScriptPolicyReportPath, nil))
		return rec.Code
	}() != http.StatusMethodNotAllowed {
		t.Fatal("GET accepted")
	}
}
