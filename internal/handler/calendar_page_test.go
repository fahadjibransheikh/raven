package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getCalendarPage(t *testing.T, h *Handler, hxHeaders map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := ownerRequest(httptest.NewRequest(http.MethodGet, "/calendar", nil))
	for k, v := range hxHeaders {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.handleCalendar(rec, req)
	return rec
}

func TestCalendarPageRendersFullDocumentForOwner(t *testing.T) {
	h, _ := newAccountOwnershipTestHandler(t)
	rec := getCalendarPage(t, h, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"<title>Calendar — Raven</title>",
		`id="mail-list"`, `data-calendar-app`, `data-cal-body`, `data-cal-mini`, `data-cal-list`,
		`id="mail-view"`, `/assets/js/calendar-app.js`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// Sidebar footer link is a live link with the same swap wiring as Mail and Contacts, not the old disabled button.
	if !strings.Contains(body, `href="/calendar"`) || !strings.Contains(body, `hx-get="/calendar"`) {
		t.Errorf("Calendar tab is not an enabled link")
	}
	if strings.Contains(body, "Calendar is coming later") || strings.Contains(body, "disabled data-sidebar-app-button") {
		t.Errorf("Calendar tab is still disabled")
	}
	if !strings.Contains(body, `data-sidebar-app-button="calendar" aria-current`) {
		t.Errorf("active Calendar footer link not marked aria-current")
	}
}

func TestCalendarPageHTMXSwapReturnsPanesForAppSwitcher(t *testing.T) {
	h, _ := newAccountOwnershipTestHandler(t)
	rec := getCalendarPage(t, h, map[string]string{"HX-Request": "true", "HX-Target": "mail-list"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	// These are the hx-select / hx-select-oob targets of the sidebar app switcher.
	for _, want := range []string{`id="mail-list"`, `id="sidebar-app-body"`, `id="sidebar-sync-controls"`, `id="mail-view"`, `id="app-pane-dialogs"`} {
		if !strings.Contains(body, want) {
			t.Errorf("swap response missing %q", want)
		}
	}
	if strings.Contains(body, "<html") {
		t.Errorf("swap response should be a fragment")
	}
}

func TestMailSidebarCalendarTabIsEnabled(t *testing.T) {
	h, _ := newAccountOwnershipTestHandler(t)
	req := ownerRequest(httptest.NewRequest(http.MethodGet, "/contacts", nil))
	rec := httptest.NewRecorder()
	h.handleContacts(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `hx-get="/calendar"`) || strings.Contains(body, "Calendar is coming later") {
		t.Errorf("Calendar tab not enabled on the contacts page")
	}
}
