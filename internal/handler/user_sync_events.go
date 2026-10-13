package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/fahadjibransheikh/raven/internal/auth"
	"github.com/fahadjibransheikh/raven/internal/mail"
	"github.com/fahadjibransheikh/raven/internal/storage"
)

// Streams share only the event bus. Snapshots are copied before writing, and
// current central ownership is checked even for accounts added after connection.
func (h *Handler) handleUserSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	ctx, owner := r.Context(), h.userID(r.Context())
	releaseActive, err := h.userIMAP.BeginActiveUserSession(ctx, owner)
	if err != nil {
		userAccountError(w, r, err)
		return
	}
	defer releaseActive()
	ch := h.userIMAP.Events().Subscribe()
	defer h.userIMAP.Events().Unsubscribe(ch)
	manual, err := h.userIMAP.ManualSyncSnapshot(ctx, owner)
	if err != nil {
		userAccountError(w, r, err)
		return
	}
	idle, err := h.userIMAP.IdleEventsForUser(ctx, owner)
	if err != nil {
		userAccountError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	_, _ = fmt.Fprint(w, "event: connected\ndata: {}\n\n")
	flusher.Flush()
	writeEvent := func(event mail.Event) bool {
		if !sseEventVisible(event, owner, map[string]bool{event.AccountID: true}, false) {
			return true
		}
		if err := h.userStorage.ValidateUser(ctx, owner); err != nil {
			return false
		}
		if event.AccountID != "" {
			state, err := h.userStorage.AccountStateForUser(ctx, owner, event.AccountID)
			if err != nil || state != storage.AccountActive {
				return true
			}
		}
		writeSSEEvent(w, flusher, event)
		return true
	}
	for _, event := range manual {
		if !writeEvent(event) {
			return
		}
	}
	for _, event := range idle {
		if !writeEvent(event) {
			return
		}
	}
	tick := time.NewTicker(1200 * time.Millisecond)
	defer tick.Stop()
	// An open stream outlives the request that authenticated it: re-check the
	// session (logout, "revoke other sessions") periodically.
	recheck := time.NewTicker(sseRecheckInterval)
	defer recheck.Stop()
	sessionToken := auth.GetSessionToken(r)
	for {
		select {
		case <-ctx.Done():
			return
		case <-recheck.C:
			if h.auth != nil && h.auth.IsEnabled() {
				if active, err := h.auth.SessionStillActive(ctx, sessionToken); err == nil && !active {
					return
				}
			}
		case <-h.userStorageContext.Done():
			return
		case <-tick.C:
			if err := h.userStorage.ValidateUser(ctx, owner); err != nil {
				return
			}
		case event := <-ch:
			if !writeEvent(event) {
				return
			}
		}
	}
}
