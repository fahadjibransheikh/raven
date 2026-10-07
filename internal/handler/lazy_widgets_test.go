package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLazyFilterCalendarRendersFromHiddenInputValue(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	h.handleLazyFilterCalendar(rec, httptest.NewRequest(http.MethodGet, "/ui/filter-calendar?name=after_date&after_date=2026-03-15", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{
		`id="mail-filter-after-calendar-wrapper"`,
		`data-tui-calendar-selected-date="2026-03-15"`,
		`name="after_date" value="2026-03-15"`,
		`id="mail-filter-after-calendar-hidden"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("calendar fragment missing %q", want)
		}
	}
}

func TestLazyFilterCalendarRejectsUnknownFieldsAndBadDates(t *testing.T) {
	h := &Handler{}
	for _, target := range []string{
		"/ui/filter-calendar",
		"/ui/filter-calendar?name=subject",
		"/ui/filter-calendar?name=%22%3E%3Cscript%3E",
		"/ui/filter-calendar?name=before_date&before_date=15/03/2026",
		"/ui/filter-calendar?name=before_date&before_date=2026-13-40",
	} {
		rec := httptest.NewRecorder()
		h.handleLazyFilterCalendar(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", target, rec.Code)
		}
	}
}

func TestLazyFilterCalendarWithoutValueLeavesNoSelection(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Handler{}).handleLazyFilterCalendar(rec, httptest.NewRequest(http.MethodGet, "/ui/filter-calendar?name=before_date&before_date=", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `data-tui-calendar-selected-date=""`) {
		t.Fatalf("status = %d, body lacks empty selection", rec.Code)
	}
}

func TestLazyScheduleAndWizardFragments(t *testing.T) {
	h := &Handler{}
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		target  string
		want    []string
		absent  []string
	}{
		{"schedule dialog", h.handleLazyComposeSchedulePanel, "/ui/compose-schedule-panel?pane=0",
			[]string{`data-compose-schedule-content`, `id="compose-schedule-calendar"`, `onclick="scheduleCompose(false, this)"`}, []string{`compose-pane-schedule`}},
		{"schedule pane", h.handleLazyComposeSchedulePanel, "/ui/compose-schedule-panel?pane=1",
			[]string{`data-compose-schedule-content`, `id="compose-pane-schedule-calendar"`, `onclick="scheduleCompose(true, this)"`}, nil},
		{"add account wizard", h.handleLazyAddAccountDialog, "/ui/add-account-dialog",
			[]string{`id="add-account-dialog"`, `id="wizard-track"`}, []string{`<script>`}},
	} {
		rec := httptest.NewRecorder()
		tc.handler(rec, httptest.NewRequest(http.MethodGet, tc.target, nil))
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s: status=%d type=%q", tc.name, rec.Code, rec.Header().Get("Content-Type"))
		}
		for _, want := range tc.want {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q", tc.name, want)
			}
		}
		for _, bad := range tc.absent {
			if strings.Contains(body, bad) {
				t.Errorf("%s: unexpected %q", tc.name, bad)
			}
		}
	}
}
