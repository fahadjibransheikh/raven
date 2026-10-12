package handler

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
)

const (
	pushWebhookMaxBody       = 64 << 10
	graphValidationTokenSize = 1024
)

// handleGmailPushHook receives Google Pub/Sub push deliveries. Anything but a bad
// token gets 204, since a non-2xx reply makes Pub/Sub redeliver indefinitely.
func (h *Handler) handleGmailPushHook(w http.ResponseWriter, r *http.Request) {
	if h.syncer == nil || !h.syncer.PushEnabled() {
		http.NotFound(w, r)
		return
	}
	if !h.syncer.PushSecretValid(r.URL.Query().Get("token")) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, pushWebhookMaxBody))
	if err != nil {
		log.Printf("gmail push: read body: %v", err)
	} else {
		h.syncer.HandleGmailPush(r.Context(), body)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGraphPushHook receives Microsoft Graph change and lifecycle notifications.
func (h *Handler) handleGraphPushHook(w http.ResponseWriter, r *http.Request) {
	if h.syncer == nil || !h.syncer.PushEnabled() {
		http.NotFound(w, r)
		return
	}
	// Subscription handshake: Graph expects the token echoed back as plain text.
	if token := r.URL.Query().Get("validationToken"); token != "" {
		if len(token) > graphValidationTokenSize {
			http.Error(w, "validation token too long", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, token)
		return
	}
	var payload struct {
		Value []struct {
			ClientState    string `json:"clientState"`
			LifecycleEvent string `json:"lifecycleEvent"`
		} `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, pushWebhookMaxBody)).Decode(&payload); err != nil || len(payload.Value) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	lifecycle := false
	for _, item := range payload.Value {
		if !h.syncer.PushSecretValid(item.ClientState) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		lifecycle = lifecycle || item.LifecycleEvent != ""
	}
	w.WriteHeader(http.StatusAccepted)
	if accountID := r.URL.Query().Get("account"); accountID != "" {
		h.syncer.HandleGraphPush(r.Context(), accountID, lifecycle)
	}
}
