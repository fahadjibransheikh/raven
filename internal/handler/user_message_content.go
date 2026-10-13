package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/fahadjibransheikh/raven/internal/mail/message"
	"github.com/fahadjibransheikh/raven/internal/providerquota"
)

// handleUserPrefetchBody downloads a message the reader is likely to open.
// It yields to requests the reader is waiting on.
func (h *Handler) handleUserPrefetchBody(w http.ResponseWriter, r *http.Request) {
	h.handleUserFetchBody(w, r.WithContext(providerquota.Background(r.Context())), false)
}

func (h *Handler) handleUserRefetchBody(w http.ResponseWriter, r *http.Request) {
	h.handleUserFetchBody(w, r, true)
}

func (h *Handler) handleUserFetchBody(w http.ResponseWriter, r *http.Request, force bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid message id", http.StatusBadRequest)
		return
	}
	if force {
		err = h.userIMAP.RefetchBody(r.Context(), h.userID(r.Context()), id)
	} else {
		err = h.userIMAP.EnsureBody(r.Context(), h.userID(r.Context()), id)
	}
	if err != nil {
		if writeMessageTooLarge(w, err) {
			return
		}
		userAccountError(w, r, err)
		return
	}
	if !force {
		h.warmStoredBodyImages(h.userID(r.Context()), id)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "refetched"})
}

// handleUserDownloadFullBody downloads a message the reader asked to see
// although it is larger than Raven downloads on its own.
func (h *Handler) handleUserDownloadFullBody(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid message id", http.StatusBadRequest)
		return
	}
	if err := h.userIMAP.EnsureFullBody(r.Context(), h.userID(r.Context()), id); err != nil {
		if writeMessageTooLarge(w, err) {
			return
		}
		userAccountError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeMessageTooLarge answers a full download that was still too large.
func writeMessageTooLarge(w http.ResponseWriter, err error) bool {
	var tooLarge *message.TooLargeError
	if !errors.As(err, &tooLarge) {
		return false
	}
	_, text, status := messageTooLargeFailure(tooLarge)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": text})
	return true
}
