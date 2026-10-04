package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/storage"
)

func seedPrimaryIdentities(t *testing.T, db *storage.DB) {
	t.Helper()
	for _, id := range []string{"victim-account", "attacker-account"} {
		tx, err := db.Write().Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := storage.SyncPrimaryIdentityTx(t.Context(), tx, id); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

func identityCall(h *Handler, owner bool, method, path string, handle http.HandlerFunc, pathValues map[string]string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range pathValues {
		req.SetPathValue(k, v)
	}
	if owner {
		req = ownerRequest(req)
	} else {
		req = attackerRequest(req)
	}
	rec := httptest.NewRecorder()
	handle(rec, req)
	return rec
}

func identityCount(t *testing.T, db *storage.DB, accountID string) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRow(`SELECT COUNT(*) FROM account_identities WHERE account_id = ?`, accountID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAccountIdentitiesOwnerFlow(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	seedPrimaryIdentities(t, db)
	pv := map[string]string{"id": "victim-account"}

	rec := identityCall(h, true, http.MethodPost, "/x", h.handleAddAccountIdentity, pv, url.Values{"email": {"Alias@Example.com"}, "name": {"Alias"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("add status = %d body = %s", rec.Code, rec.Body)
	}
	var resp struct {
		Identities []struct {
			ID      int64  `json:"id"`
			Email   string `json:"email"`
			Source  string `json:"source"`
			Default bool   `json:"is_default"`
		} `json:"identities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Identities) != 2 {
		t.Fatalf("add response = %s (%v)", rec.Body, err)
	}
	alias := resp.Identities[1]
	if alias.Email != "alias@example.com" || alias.Source != "manual" {
		t.Fatalf("alias = %+v", alias)
	}

	if rec := identityCall(h, true, http.MethodPost, "/x", h.handleAddAccountIdentity, pv, url.Values{"email": {"alias@example.com"}}); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d", rec.Code)
	}
	if rec := identityCall(h, true, http.MethodPost, "/x", h.handleAddAccountIdentity, pv, url.Values{"email": {"nope"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d", rec.Code)
	}

	idPV := map[string]string{"id": "victim-account", "identity": itoa(alias.ID)}
	if rec := identityCall(h, true, http.MethodPost, "/x", h.handleSetDefaultAccountIdentity, idPV, nil); rec.Code != http.StatusOK {
		t.Fatalf("default status = %d", rec.Code)
	}
	if rec := identityCall(h, true, http.MethodPost, "/x", h.handleDeleteAccountIdentity, idPV, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if n := identityCount(t, db, "victim-account"); n != 1 {
		t.Fatalf("identities after delete = %d, want primary only", n)
	}
	// Primary is protected.
	var primaryID int64
	_ = db.Read().QueryRow(`SELECT id FROM account_identities WHERE account_id = 'victim-account'`).Scan(&primaryID)
	if rec := identityCall(h, true, http.MethodPost, "/x", h.handleDeleteAccountIdentity, map[string]string{"id": "victim-account", "identity": itoa(primaryID)}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("delete primary status = %d", rec.Code)
	}
	// Non-Gmail account: refresh is rejected without touching a provider.
	if rec := identityCall(h, true, http.MethodPost, "/x", h.handleRefreshAccountIdentities, pv, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("refresh imap status = %d", rec.Code)
	}
}

func TestAccountIdentitiesHTMXRendersSection(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	seedPrimaryIdentities(t, db)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("HX-Request", "true")
	req.SetPathValue("id", "victim-account")
	rec := httptest.NewRecorder()
	h.handleListAccountIdentities(rec, ownerRequest(req))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `data-account-identities="victim-account"`) || !strings.Contains(body, "owner@example.com") {
		t.Fatalf("htmx list = %d %s", rec.Code, body)
	}
}

func TestAccountIdentitiesForeignUserGets404WithNoSideEffects(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	seedPrimaryIdentities(t, db)
	mine, err := db.AddManualIdentity(t.Context(), "owner", "victim-account", "alias@example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	before := identityCount(t, db, "victim-account")
	pv := map[string]string{"id": "victim-account"}
	idPV := map[string]string{"id": "victim-account", "identity": itoa(mine.ID)}

	calls := []struct {
		name   string
		handle http.HandlerFunc
		pv     map[string]string
		form   url.Values
	}{
		{"list", h.handleListAccountIdentities, pv, nil},
		{"add", h.handleAddAccountIdentity, pv, url.Values{"email": {"evil@example.com"}}},
		{"delete", h.handleDeleteAccountIdentity, idPV, nil},
		{"default", h.handleSetDefaultAccountIdentity, idPV, nil},
		{"refresh", h.handleRefreshAccountIdentities, pv, nil},
		{"dismiss", h.handleDismissIdentitySuggestion, pv, url.Values{"email": {"x@example.com"}}},
	}
	for _, c := range calls {
		rec := identityCall(h, false, http.MethodPost, "/x", c.handle, c.pv, c.form)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", c.name, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "alias@example.com") || strings.Contains(rec.Body.String(), "owner@example.com") {
			t.Errorf("%s: leaked identity data: %s", c.name, rec.Body)
		}
	}
	if got := identityCount(t, db, "victim-account"); got != before {
		t.Fatalf("identity count changed %d -> %d", before, got)
	}
	var dismissals, defaults int
	_ = db.Read().QueryRow(`SELECT COUNT(*) FROM account_identity_dismissals`).Scan(&dismissals)
	_ = db.Read().QueryRow(`SELECT COUNT(*) FROM account_identities WHERE account_id = 'victim-account' AND is_default = 1 AND id = ?`, mine.ID).Scan(&defaults)
	if dismissals != 0 || defaults != 0 {
		t.Fatalf("foreign request had side effects: dismissals=%d alias-default=%d", dismissals, defaults)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
