package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/cristianadrielbraun/gofer/internal/storage"
)

// handleEmptyFolder permanently deletes every message in a Spam or Trash
// folder (or, for the unified folder, in each owned account's). It reuses the
// per-message permanent-delete queue, so IMAP, Gmail and Outlook are each
// cleared by their existing delete path in applyRemoteMessageMutation.
func (h *Handler) handleEmptyFolder(w http.ResponseWriter, r *http.Request) {
	ctx := context.WithoutCancel(r.Context())
	targets, err := h.db.FolderEmptyTargets(ctx, h.userID(ctx), r.PathValue("id"))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, "folder not found", http.StatusNotFound)
		return
	case errors.Is(err, storage.ErrFolderEmptyUnsupported):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	deleted := 0
	for _, t := range targets {
		if len(t.MessageIDs) == 0 {
			continue
		}
		if err := h.db.PermanentlyDeleteMessagesAndQueueForUser(ctx, t.MessageIDs, t.FolderID, h.userID(ctx)); err != nil {
			http.Error(w, strings.TrimSpace(err.Error()), http.StatusInternalServerError)
			return
		}
		deleted += len(t.MessageIDs)
		h.publishMutation(t.AccountID, t.FolderID)
	}
	h.signalMessageMutationWorker()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{"deleted": deleted})
}
