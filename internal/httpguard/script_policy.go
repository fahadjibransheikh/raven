package httpguard

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Raven's pages may run only scripts loaded from Raven's own files: no
// inline scripts, no inline event handlers, no eval. If HTML is ever injected
// into a page, the browser then refuses to run it. Responses with their own
// policy, such as message bodies and message files, replace it.
//
// The browser reports what the policy blocks to ScriptPolicyReportPath, and
// Raven logs it, so a page that still depends on inline code shows up as a
// log line rather than only as a button that does nothing.
const (
	ScriptPolicyReportPath = "/api/csp-report"
	pagePolicy             = "frame-ancestors 'self'; object-src 'none'; base-uri 'self'"
	scriptPolicy           = "script-src 'self'; report-uri " + ScriptPolicyReportPath
)

// PagePolicy is the Content-Security-Policy of Raven's own pages.
const PagePolicy = pagePolicy + "; " + scriptPolicy

func setPagePolicy(headers http.Header) {
	headers.Set("Content-Security-Policy", PagePolicy)
}

// At most this many reports are logged a minute, so a page can't flood the log.
const scriptPolicyReportsPerMinute = 30

var scriptPolicyReports struct {
	sync.Mutex
	window time.Time
	count  int
}

func handleScriptPolicyReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	w.WriteHeader(http.StatusNoContent)
	if err != nil {
		return
	}
	var report struct {
		Report struct {
			DocumentURI        string `json:"document-uri"`
			BlockedURI         string `json:"blocked-uri"`
			ViolatedDirective  string `json:"violated-directive"`
			EffectiveDirective string `json:"effective-directive"`
			SourceFile         string `json:"source-file"`
			LineNumber         int    `json:"line-number"`
			ScriptSample       string `json:"script-sample"`
		} `json:"csp-report"`
	}
	if json.Unmarshal(body, &report) != nil {
		return
	}
	scriptPolicyReports.Lock()
	now := time.Now()
	if now.Sub(scriptPolicyReports.window) > time.Minute {
		scriptPolicyReports.window, scriptPolicyReports.count = now, 0
	}
	scriptPolicyReports.count++
	allowed := scriptPolicyReports.count <= scriptPolicyReportsPerMinute
	scriptPolicyReports.Unlock()
	if !allowed {
		return
	}
	c := report.Report
	directive := c.EffectiveDirective
	if directive == "" {
		directive = c.ViolatedDirective
	}
	log.Printf("script policy: %s blocked %s on %s (%s:%d) %q",
		reportField(directive, 60), reportField(c.BlockedURI, 200), reportField(c.DocumentURI, 200),
		reportField(c.SourceFile, 200), c.LineNumber, reportField(c.ScriptSample, 80))
}

// reportField shortens a value from a report and keeps it on one log line.
func reportField(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	if len(value) > limit {
		value = value[:limit] + "…"
	}
	return value
}
