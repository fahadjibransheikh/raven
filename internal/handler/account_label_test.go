package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/storage"
)

func postAccountLabel(t *testing.T, h *Handler, accountID, label string, owner bool) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"label": {label}}
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+accountID+"/label", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", accountID)
	rec := httptest.NewRecorder()
	if owner {
		req = ownerRequest(req)
	} else {
		req = attackerRequest(req)
	}
	h.handleUpdateAccountLabel(rec, req)
	return rec
}

func storedAccountLabel(t *testing.T, db *storage.DB, accountID string) string {
	t.Helper()
	var label string
	if err := db.Read().QueryRowContext(t.Context(), `SELECT label FROM accounts WHERE id = ?`, accountID).Scan(&label); err != nil {
		t.Fatalf("read label: %v", err)
	}
	return label
}

func TestUpdateAccountLabelOwnerCanSetTrimAndClear(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)

	rec := postAccountLabel(t, h, "victim-account", "  Work  ", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["label"] != "Work" || resp["display_label"] != "Work" {
		t.Fatalf("response = %v", resp)
	}
	if got := storedAccountLabel(t, db, "victim-account"); got != "Work" {
		t.Fatalf("stored label = %q, want Work", got)
	}

	rec = postAccountLabel(t, h, "victim-account", "   ", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d body = %q", rec.Code, rec.Body.String())
	}
	resp = map[string]string{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["label"] != "" || resp["display_label"] != "Owner" {
		t.Fatalf("clear response = %v, want empty label falling back to name", resp)
	}
	if got := storedAccountLabel(t, db, "victim-account"); got != "" {
		t.Fatalf("stored label after clear = %q", got)
	}
}

func TestUpdateAccountLabelRejectsForeignUser(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	rec := postAccountLabel(t, h, "victim-account", "Hijacked", false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %q, want 404", rec.Code, rec.Body.String())
	}
	if got := storedAccountLabel(t, db, "victim-account"); got != "" {
		t.Fatalf("foreign user changed label to %q", got)
	}
}

func TestUpdateAccountLabelLengthLimit(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)

	ok := strings.Repeat("é", 64) // 64 runes, 128 bytes
	if rec := postAccountLabel(t, h, "victim-account", ok, true); rec.Code != http.StatusOK {
		t.Fatalf("64 runes: status = %d body = %q", rec.Code, rec.Body.String())
	}
	if got := storedAccountLabel(t, db, "victim-account"); got != ok {
		t.Fatalf("stored label = %q", got)
	}

	if rec := postAccountLabel(t, h, "victim-account", ok+"x", true); rec.Code != http.StatusBadRequest {
		t.Fatalf("65 runes: status = %d, want 400", rec.Code)
	}
	if rec := postAccountLabel(t, h, "victim-account", "a\nb", true); rec.Code != http.StatusBadRequest {
		t.Fatalf("control char: status = %d, want 400", rec.Code)
	}
	if got := storedAccountLabel(t, db, "victim-account"); got != ok {
		t.Fatalf("rejected input changed label to %q", got)
	}
}

// The label is display-only: a scheduled/queued draft must still go out with
// the account's display_name as the From name.
func TestAccountLabelDoesNotChangeOutgoingFrom(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	messageID := seedOwnedDraftWithRecipient(t, db)

	before, err := h.outgoingMessageFromDraft(t.Context(), messageID)
	if err != nil {
		t.Fatalf("outgoingMessageFromDraft() error = %v", err)
	}
	if rec := postAccountLabel(t, h, "victim-account", "Sidebar nickname", true); rec.Code != http.StatusOK {
		t.Fatalf("set label status = %d", rec.Code)
	}
	after, err := h.outgoingMessageFromDraft(t.Context(), messageID)
	if err != nil {
		t.Fatalf("outgoingMessageFromDraft() error = %v", err)
	}
	if before.FromName != "Owner" || after.FromName != "Owner" || after.FromEmail != "owner@example.com" {
		t.Fatalf("From = %q <%s> (before %q), want Owner <owner@example.com>", after.FromName, after.FromEmail, before.FromName)
	}
}

func seedOwnedDraftWithRecipient(t *testing.T, db *storage.DB) int64 {
	t.Helper()
	if err := db.UpsertFolders(t.Context(), []storage.UpsertFolderInput{
		{ID: "victim-drafts", AccountID: "victim-account", RemoteID: "Drafts", Name: "Drafts", Role: "drafts", Selectable: true},
	}); err != nil {
		t.Fatalf("UpsertFolders() error = %v", err)
	}
	messageID, err := db.SaveDraftMessage(t.Context(), storage.DraftMessageInput{
		AccountID:         "victim-account",
		FolderID:          "victim-drafts",
		InternetMessageID: "<label-draft@example.com>",
		Subject:           "Label draft",
		FromEmail:         "owner@example.com",
		ToRecipients:      []storage.Recipient{{Email: "to@example.com"}},
		Date:              time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SaveDraftMessage() error = %v", err)
	}
	return messageID
}
