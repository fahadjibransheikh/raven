package handler

import (
	"net/http"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/views"
)

// Fragments for widgets that are hidden until opened (filter date pickers,
// compose scheduler, add-account wizard). Pages carry a small placeholder and
// fetch these the first time the widget becomes visible, which keeps ~170 KB of
// markup out of every page load.

// lazyFilterCalendars is the whitelist of filter date fields; the element id
// comes from here so request input never reaches a template as an identifier.
var lazyFilterCalendars = map[string]string{
	"after_date":  "mail-filter-after-calendar",
	"before_date": "mail-filter-before-calendar",
}

func (h *Handler) handleLazyFilterCalendar(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	id, ok := lazyFilterCalendars[name]
	if !ok {
		http.Error(w, "unknown calendar", http.StatusBadRequest)
		return
	}
	// The placeholder includes its hidden input, so the current selection
	// arrives as a parameter named after the field.
	var value *time.Time
	if raw := r.URL.Query().Get(name); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			http.Error(w, "invalid date", http.StatusBadRequest)
			return
		}
		value = &parsed
	}
	writeLazyFragment(w)
	views.FilterCalendar(id, name, value).Render(r.Context(), w)
}

func (h *Handler) handleLazyComposeSchedulePanel(w http.ResponseWriter, r *http.Request) {
	writeLazyFragment(w)
	views.ComposeSchedulePanel(r.URL.Query().Get("pane") == "1").Render(r.Context(), w)
}

func (h *Handler) handleLazyAddAccountDialog(w http.ResponseWriter, r *http.Request) {
	writeLazyFragment(w)
	views.AddAccountDialogContent().Render(r.Context(), w)
}

func writeLazyFragment(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
}
