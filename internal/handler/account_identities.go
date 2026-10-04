package handler

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/cristianadrielbraun/gofer/internal/models"
	"github.com/cristianadrielbraun/gofer/internal/storage"
	"github.com/cristianadrielbraun/gofer/internal/views"
)

// Account sending-address endpoints. Every handler first requires an owned
// account (foreign and missing both 404 before any write or provider call);
// the storage methods re-check ownership in SQL.
//
// htmx requests get the settings section re-rendered; other callers get JSON.
// For htmx, operational errors are rendered inline with 200 because htmx does
// not swap error statuses; ownership failures stay 404.

type accountIdentitiesJSON struct {
	Identities  []models.AccountIdentity    `json:"identities"`
	Suggestions []models.IdentitySuggestion `json:"suggestions"`
}

func (h *Handler) writeAccountIdentities(w http.ResponseWriter, r *http.Request, accountID string, status int, message string) {
	ctx := r.Context()
	userID := h.userID(ctx)
	account, err := h.ownedAccount(ctx, accountID)
	if err != nil || account == nil {
		http.NotFound(w, r)
		return
	}
	identities, err := h.db.ListAccountIdentities(ctx, userID, accountID)
	if err != nil {
		log.Printf("list identities account=%s: %v", accountID, err)
		http.Error(w, "failed to load sending addresses", http.StatusInternalServerError)
		return
	}
	suggestions, err := h.db.IdentitySuggestions(ctx, userID, accountID)
	if err != nil {
		log.Printf("identity suggestions account=%s: %v", accountID, err)
		suggestions = nil
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html")
		views.SettingsAccountIdentities(views.AccountIdentitiesData{
			Account: *account, Identities: identities, Suggestions: suggestions, Error: message,
		}).Render(ctx, w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{"identities": identities, "suggestions": suggestions}
	if message != "" {
		body["error"] = message
	}
	if identities == nil {
		body["identities"] = []models.AccountIdentity{}
	}
	if suggestions == nil {
		body["suggestions"] = []models.IdentitySuggestion{}
	}
	json.NewEncoder(w).Encode(body)
}

// identityFailure maps a storage error to (status, user message).
func identityFailure(err error) (int, string) {
	switch {
	case errors.Is(err, storage.ErrIdentityInvalid):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, storage.ErrIdentityDuplicate):
		return http.StatusConflict, err.Error()
	case errors.Is(err, storage.ErrIdentityProtected):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, storage.ErrIdentityNotFound):
		return http.StatusNotFound, "sending address not found"
	}
	log.Printf("account identity: %v", err)
	return http.StatusInternalServerError, "could not update sending addresses"
}

func (h *Handler) finishIdentityMutation(w http.ResponseWriter, r *http.Request, accountID string, err error) {
	if err == nil {
		h.writeAccountIdentities(w, r, accountID, http.StatusOK, "")
		return
	}
	status, msg := identityFailure(err)
	h.writeAccountIdentities(w, r, accountID, status, msg)
}

func (h *Handler) ownedIdentityRoute(w http.ResponseWriter, r *http.Request) (string, bool) {
	accountID := r.PathValue("id")
	if accountID == "" || !h.requireOwnedAccount(w, r, accountID) {
		return "", false
	}
	return accountID, true
}

func (h *Handler) handleListAccountIdentities(w http.ResponseWriter, r *http.Request) {
	if accountID, ok := h.ownedIdentityRoute(w, r); ok {
		h.writeAccountIdentities(w, r, accountID, http.StatusOK, "")
	}
}

func (h *Handler) handleAddAccountIdentity(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.ownedIdentityRoute(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.writeAccountIdentities(w, r, accountID, http.StatusBadRequest, "invalid form data")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if len(name) > 100 {
		h.writeAccountIdentities(w, r, accountID, http.StatusBadRequest, "name must be 100 characters or fewer")
		return
	}
	_, err := h.db.AddManualIdentity(r.Context(), h.userID(r.Context()), accountID, r.FormValue("email"), name)
	h.finishIdentityMutation(w, r, accountID, err)
}

func (h *Handler) handleDeleteAccountIdentity(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.ownedIdentityRoute(w, r)
	if !ok {
		return
	}
	identityID, err := strconv.ParseInt(r.PathValue("identity"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	err = h.db.DeleteIdentity(r.Context(), h.userID(r.Context()), accountID, identityID)
	h.finishIdentityMutation(w, r, accountID, err)
}

func (h *Handler) handleSetDefaultAccountIdentity(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.ownedIdentityRoute(w, r)
	if !ok {
		return
	}
	identityID, err := strconv.ParseInt(r.PathValue("identity"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	err = h.db.SetDefaultIdentity(r.Context(), h.userID(r.Context()), accountID, identityID)
	h.finishIdentityMutation(w, r, accountID, err)
}

func (h *Handler) handleDismissIdentitySuggestion(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.ownedIdentityRoute(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.writeAccountIdentities(w, r, accountID, http.StatusBadRequest, "invalid form data")
		return
	}
	err := h.db.DismissIdentitySuggestion(r.Context(), h.userID(r.Context()), accountID, r.FormValue("email"))
	h.finishIdentityMutation(w, r, accountID, err)
}

// handleRefreshAccountIdentities re-reads provider aliases now. Only Gmail
// exposes them; Outlook and IMAP accounts add addresses manually.
func (h *Handler) handleRefreshAccountIdentities(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.ownedIdentityRoute(w, r)
	if !ok {
		return
	}
	account, err := h.ownedAccount(r.Context(), accountID)
	if err != nil || account == nil {
		http.NotFound(w, r)
		return
	}
	if account.Provider != "gmail" {
		h.writeAccountIdentities(w, r, accountID, http.StatusBadRequest, "this account's provider does not list sending addresses; add them manually")
		return
	}
	if h.syncer == nil {
		h.writeAccountIdentities(w, r, accountID, http.StatusServiceUnavailable, "mail sync is not available")
		return
	}
	if err := h.syncer.RefreshGmailIdentities(r.Context(), accountID); err != nil {
		log.Printf("refresh gmail identities account=%s: %v", accountID, err)
		h.writeAccountIdentities(w, r, accountID, http.StatusBadGateway, "Could not refresh from Gmail. Try again later.")
		return
	}
	h.writeAccountIdentities(w, r, accountID, http.StatusOK, "")
}
