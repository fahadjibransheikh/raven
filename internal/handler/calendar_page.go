package handler

import (
	"net/http"

	"github.com/cristianadrielbraun/gofer/internal/views"
)

// handleCalendar renders the calendar app shell. Events and calendars are
// loaded by assets/js/calendar-app.js from /api/calendar, so the page needs
// only the account list (for the reconnect form and the sidebar chrome).
func (h *Handler) handleCalendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := h.userID(ctx)
	ctx = h.contextWithUserTimezone(ctx, r, userID)
	uiSettings := h.db.GetUISettings(ctx, userID)
	accounts, _ := h.db.GetAccounts(ctx, userID)

	w.Header().Set("Content-Type", "text/html")
	if r.Header.Get("HX-Request") == "true" {
		switch r.Header.Get("HX-Target") {
		case "mail-list":
			views.CalendarAppPartial(accounts).Render(ctx, w)
		case "app-shell":
			views.CalendarShell(accounts, uiSettings).Render(ctx, w)
		default:
			views.CalendarPage(accounts).Render(ctx, w)
		}
		return
	}
	views.CalendarLayout(accounts, uiSettings).Render(ctx, w)
}
