package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-message/mail"

	mailpkg "github.com/cristianadrielbraun/gofer/internal/mail"
	"github.com/cristianadrielbraun/gofer/internal/models"
	"github.com/cristianadrielbraun/gofer/internal/storage"
)

type composeIdentityFixture struct {
	h  *Handler
	db *storage.DB
}

// newComposeIdentityFixture gives owner two accounts: victim-account (primary
// owner@example.com, alias sales@example.com "Sales") and second-account
// (second@example.com, alias other@example.com). attacker-account has its own alias.
func newComposeIdentityFixture(t *testing.T) composeIdentityFixture {
	t.Helper()
	h, db := newAccountOwnershipTestHandler(t)
	h.syncer = mailpkg.NewSyncOrchestrator(db, h.accountStore, h.blobStore, nil)
	if _, err := db.Write().ExecContext(t.Context(), `
		INSERT INTO accounts (id, user_id, provider, email_address, display_name,
			imap_host, imap_port, imap_tls_mode, smtp_host, smtp_port, smtp_tls_mode, username)
		VALUES ('second-account', 'owner', 'imap', 'second@example.com', 'Second',
			'127.0.0.1', 1, 'tls', '127.0.0.1', 1, 'tls', 'second@example.com')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"victim-account", "attacker-account", "second-account"} {
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
	for _, a := range []struct{ user, account, email, name string }{
		{"owner", "victim-account", "Sales@Example.com", "Sales"},
		{"owner", "second-account", "other@example.com", ""},
		{"attacker", "attacker-account", "evil@example.com", "Evil"},
	} {
		if _, err := db.AddManualIdentity(t.Context(), a.user, a.account, a.email, a.name); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.UpsertFolders(t.Context(), []storage.UpsertFolderInput{
		{ID: "victim-drafts", AccountID: "victim-account", RemoteID: "Drafts", Name: "Drafts", Role: "drafts", Selectable: true},
		{ID: "second-drafts", AccountID: "second-account", RemoteID: "Drafts", Name: "Drafts", Role: "drafts", Selectable: true},
	}); err != nil {
		t.Fatal(err)
	}
	return composeIdentityFixture{h, db}
}

func (f composeIdentityFixture) post(handle http.HandlerFunc, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/compose", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handle(rec, ownerRequest(req))
	return rec
}

func composeForm(accountID, fromEmail string) url.Values {
	form := url.Values{
		"account_id": {accountID},
		"to":         {"recipient@example.com"},
		"subject":    {"Identity"},
		"body":       {"hello"},
		"draft_id":   {"<ident-draft@example.com>"},
	}
	if fromEmail != "" {
		form.Set("from_email", fromEmail)
	}
	return form
}

func scheduleForm(accountID, fromEmail string) url.Values {
	form := composeForm(accountID, fromEmail)
	at := time.Now().Add(48 * time.Hour)
	form.Set("schedule_date", at.Format("2006-01-02"))
	form.Set("schedule_hour", "10")
	form.Set("schedule_minute", "0")
	form.Set("schedule_timezone", "UTC")
	return form
}

func mimeFrom(t *testing.T, raw []byte) *mail.Address {
	t.Helper()
	reader, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse MIME: %v", err)
	}
	defer reader.Close()
	from, err := reader.Header.AddressList("From")
	if err != nil || len(from) != 1 {
		t.Fatalf("MIME From = %v, %v", from, err)
	}
	return from[0]
}

func (f composeIdentityFixture) storedDraftFrom(t *testing.T, accountID string) (name, email string, found bool) {
	t.Helper()
	id, _ := f.db.GetMessageLocalIDByInternetIDInternal(t.Context(), accountID, "<ident-draft@example.com>")
	if id == 0 {
		return "", "", false
	}
	email2, err := f.db.GetEmailByIDInternal(t.Context(), strconv.FormatInt(id, 10))
	if err != nil || email2 == nil {
		t.Fatalf("load draft: %v", err)
	}
	return email2.From.Name, email2.From.Email, true
}

func (f composeIdentityFixture) countRows(t *testing.T, query string) int {
	t.Helper()
	var n int
	if err := f.db.Read().QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestComposeDraftSaveStoresValidAlias(t *testing.T) {
	f := newComposeIdentityFixture(t)
	if rec := f.post(f.h.handleComposeDraft, composeForm("victim-account", "sales@example.com")); rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
	name, email, ok := f.storedDraftFrom(t, "victim-account")
	if !ok || name != "Sales" || email != "sales@example.com" {
		t.Fatalf("stored draft From = %q <%s> (found %v), want Sales <sales@example.com>", name, email, ok)
	}
}

func TestComposeSendUsesAliasInMIMEAndKeepsEnvelopeFromAsAccount(t *testing.T) {
	f := newComposeIdentityFixture(t)
	rec := f.post(f.h.handleCompose, composeForm("victim-account", "sales@example.com"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	queued, err := f.db.GetOutgoingSend(t.Context(), resp["send_id"])
	if err != nil {
		t.Fatal(err)
	}
	from := mimeFrom(t, queued.MIMEData)
	if from.Address != "sales@example.com" || from.Name != "Sales" {
		t.Fatalf("MIME From = %v, want Sales <sales@example.com>", from)
	}
	if queued.EnvelopeFrom != "owner@example.com" {
		t.Fatalf("envelope-from = %q, want the account login address", queued.EnvelopeFrom)
	}
}

func TestComposeScheduleKeepsAlias(t *testing.T) {
	f := newComposeIdentityFixture(t)
	rec := f.post(f.h.handleComposeSchedule, scheduleForm("victim-account", "sales@example.com"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
	if name, email, _ := f.storedDraftFrom(t, "victim-account"); name != "Sales" || email != "sales@example.com" {
		t.Fatalf("scheduled draft From = %q <%s>", name, email)
	}
	id, _ := f.db.GetMessageLocalIDByInternetIDInternal(t.Context(), "victim-account", "<ident-draft@example.com>")
	queued, err := f.db.OutgoingSendForMessageInternal(t.Context(), id)
	if err != nil || queued == nil {
		t.Fatalf("scheduled send: %v, %v", queued, err)
	}
	if from := mimeFrom(t, queued.MIMEData); from.Address != "sales@example.com" || from.Name != "Sales" {
		t.Fatalf("scheduled MIME From = %v", from)
	}
	if queued.EnvelopeFrom != "owner@example.com" {
		t.Fatalf("scheduled envelope-from = %q", queued.EnvelopeFrom)
	}
}

func TestComposeOmittedFromEmailUsesDefaultIdentity(t *testing.T) {
	f := newComposeIdentityFixture(t)
	send := func() *mail.Address {
		rec := f.post(f.h.handleCompose, composeForm("victim-account", ""))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
		}
		var resp map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		queued, _ := f.db.GetOutgoingSend(t.Context(), resp["send_id"])
		return mimeFrom(t, queued.MIMEData)
	}
	if from := send(); from.Address != "owner@example.com" || from.Name != "Owner" {
		t.Fatalf("default From = %v, want Owner <owner@example.com>", from)
	}
	ids, _ := f.db.ListAccountIdentities(t.Context(), "owner", "victim-account")
	for _, id := range ids {
		if id.Email == "sales@example.com" {
			if err := f.db.SetDefaultIdentity(t.Context(), "owner", "victim-account", id.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if from := send(); from.Address != "sales@example.com" {
		t.Fatalf("From after changing default = %v, want the new default", from)
	}
}

func TestComposeRejectsForeignAndUnknownFromEmail(t *testing.T) {
	f := newComposeIdentityFixture(t)
	cases := map[string]string{
		"unknown address":                  "nobody@example.com",
		"another user's alias":             "evil@example.com",
		"own alias of a different account": "other@example.com",
		"other account's primary":          "second@example.com",
		"malformed":                        "not an address",
	}
	for name, from := range cases {
		for endpoint, call := range map[string]func() *httptest.ResponseRecorder{
			"draft": func() *httptest.ResponseRecorder {
				return f.post(f.h.handleComposeDraft, composeForm("victim-account", from))
			},
			"send": func() *httptest.ResponseRecorder {
				return f.post(f.h.handleCompose, composeForm("victim-account", from))
			},
			"schedule": func() *httptest.ResponseRecorder {
				return f.post(f.h.handleComposeSchedule, scheduleForm("victim-account", from))
			},
		} {
			if rec := call(); rec.Code != http.StatusBadRequest {
				t.Errorf("%s/%s: status = %d body = %q, want 400", name, endpoint, rec.Code, rec.Body.String())
			}
		}
	}
	if n := f.countRows(t, `SELECT COUNT(*) FROM outgoing_sends`); n != 0 {
		t.Fatalf("%d sends queued by rejected requests", n)
	}
	if _, _, found := f.storedDraftFrom(t, "victim-account"); found {
		t.Fatal("a rejected request saved a draft")
	}
}

func TestPickReplyIdentity(t *testing.T) {
	ids := []models.AccountIdentity{
		{Email: "me@example.com", IsDefault: true},
		{Email: "sales@example.com"},
		{Email: "support@example.com"},
	}
	c := func(emails ...string) []models.Contact {
		var out []models.Contact
		for _, e := range emails {
			out = append(out, models.Contact{Email: e})
		}
		return out
	}
	for _, tc := range []struct {
		name, role, want string
		from             string
		to, cc           []models.Contact
	}{
		{"To alias", "inbox", "sales@example.com", "x@y.com", c("Other@y.com", "Sales@Example.com"), nil},
		{"To beats Cc", "inbox", "support@example.com", "x@y.com", c("support@example.com"), c("sales@example.com")},
		{"Cc alias", "inbox", "sales@example.com", "x@y.com", c("list@y.com"), c("sales@example.com")},
		{"no match uses default", "inbox", "me@example.com", "x@y.com", c("list@y.com"), nil},
		{"sent uses From", "sent", "support@example.com", "support@example.com", c("sales@example.com"), nil},
		{"sent without match uses default", "sent", "me@example.com", "gone@example.com", c("sales@example.com"), nil},
	} {
		got := pickReplyIdentity(ids, tc.role, models.Contact{Email: tc.from}, tc.to, tc.cc)
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := pickReplyIdentity(nil, "inbox", models.Contact{}, c("a@b.c"), nil); got != "" {
		t.Errorf("no identities: got %q, want empty", got)
	}
}
