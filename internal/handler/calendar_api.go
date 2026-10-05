package handler

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/models"
	"github.com/cristianadrielbraun/gofer/internal/storage"
)

// Calendar read API (C1a). Every query is scoped to the request user in SQL;
// a foreign calendar id answers 404 exactly like a missing one.

const calendarMaxRange = 120 * 24 * time.Hour

func writeCalendarJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

type calendarAccountJSON struct {
	AccountID      string            `json:"account_id"`
	Email          string            `json:"email"`
	NeedsReconnect bool              `json:"needs_reconnect"`
	LastError      string            `json:"last_error,omitempty"`
	LastSyncedAt   *time.Time        `json:"last_synced_at,omitempty"`
	Calendars      []models.Calendar `json:"calendars"`
}

func (h *Handler) handleListCalendars(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := h.userID(ctx)
	accounts, err := h.db.ListCalendarAccounts(ctx, userID)
	if err == nil {
		var calendars []models.Calendar
		var states map[string]models.CalendarAccountState
		if calendars, err = h.db.ListCalendarsForUser(ctx, userID); err == nil {
			if states, err = h.db.ListCalendarAccountStates(ctx, userID); err == nil {
				out := make([]calendarAccountJSON, 0, len(accounts))
				byAccount := map[string]*calendarAccountJSON{}
				for _, a := range accounts {
					st := states[a.ID]
					out = append(out, calendarAccountJSON{AccountID: a.ID, Email: a.Email, NeedsReconnect: st.NeedsReconnect,
						LastError: st.LastError, LastSyncedAt: st.LastSyncedAt, Calendars: []models.Calendar{}})
					byAccount[a.ID] = &out[len(out)-1]
				}
				for _, c := range calendars {
					if acc := byAccount[c.AccountID]; acc != nil {
						acc.Calendars = append(acc.Calendars, c)
					}
				}
				writeCalendarJSON(w, http.StatusOK, map[string]any{"accounts": out})
				return
			}
		}
	}
	log.Printf("list calendars user=%s: %v", userID, err)
	http.Error(w, "failed to load calendars", http.StatusInternalServerError)
}

func (h *Handler) handleSetCalendarSelected(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form data", http.StatusBadRequest)
		return
	}
	var selected bool
	switch r.PostFormValue("selected") {
	case "1":
		selected = true
	case "0":
	default:
		http.Error(w, "selected must be 0 or 1", http.StatusBadRequest)
		return
	}
	err = h.db.SetCalendarSelected(r.Context(), h.userID(r.Context()), id, selected)
	if errors.Is(err, storage.ErrCalendarNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("set calendar selected id=%d: %v", id, err)
		http.Error(w, "failed to update calendar", http.StatusInternalServerError)
		return
	}
	writeCalendarJSON(w, http.StatusOK, map[string]any{"id": id, "selected": selected})
}

func (h *Handler) handleListCalendarEvents(w http.ResponseWriter, r *http.Request) {
	start, err1 := time.Parse(time.RFC3339, r.URL.Query().Get("start"))
	end, err2 := time.Parse(time.RFC3339, r.URL.Query().Get("end"))
	if err1 != nil || err2 != nil || !end.After(start) {
		http.Error(w, "start and end must be RFC3339 with end after start", http.StatusBadRequest)
		return
	}
	if end.Sub(start) > calendarMaxRange {
		http.Error(w, "range must not exceed 120 days", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	userID := h.userID(ctx)
	// All-day events are dates, so "which day is this range" needs the user's
	// zone; fall back to the offset the client used in start.
	loc := start.Location()
	if tz := strings.TrimSpace(h.db.GetUISettings(ctx, userID)["timezone"]); tz != "" && tz != "local" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	events, err := h.db.ListCalendarEvents(ctx, userID, start, end, loc)
	if err != nil {
		log.Printf("list calendar events user=%s: %v", userID, err)
		http.Error(w, "failed to load events", http.StatusInternalServerError)
		return
	}
	writeCalendarJSON(w, http.StatusOK, map[string]any{
		"start": start.UTC(), "end": end.UTC(), "events": events,
	})
}

type calendarSyncResultJSON struct {
	AccountID string `json:"account_id"`
	Status    string `json:"status"` // ok, error, running
	Error     string `json:"error,omitempty"`
}

func (h *Handler) handleSyncCalendars(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accounts, err := h.db.ListCalendarAccounts(ctx, h.userID(ctx))
	if err != nil {
		http.Error(w, "failed to load accounts", http.StatusInternalServerError)
		return
	}
	results := make([]calendarSyncResultJSON, 0, len(accounts))
	for _, a := range accounts {
		res := calendarSyncResultJSON{AccountID: a.ID, Status: "ok"}
		switch err := h.SyncCalendarAccount(ctx, a.ID); {
		case errors.Is(err, errCalendarSyncAlreadyRunning):
			res.Status = "running"
		case err != nil:
			log.Printf("calendar sync %s: %v", a.ID, err)
			res.Status, res.Error = "error", "sync failed"
			if isGoogleCalendarScopeError(err) {
				res.Error = "needs_reconnect"
			}
		}
		results = append(results, res)
	}
	writeCalendarJSON(w, http.StatusOK, map[string]any{"accounts": results})
}
