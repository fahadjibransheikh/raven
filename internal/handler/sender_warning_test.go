package handler

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/auth"
	"github.com/fahadjibransheikh/raven/internal/storage"
)

func TestSenderWarnings(t *testing.T) {
	known := map[string]int{"paypal.com": 40, "gmail.com": 500, "acme.io": 12, "gmx.com": 30}
	tests := []struct {
		name      string
		fromName  string
		fromEmail string
		known     map[string]int
		want      string // substring of the single expected warning; "" means none
	}{
		{"name shows other address", "support@paypal.com", "x@evil.io", known, "shows a different address (support@paypal.com)"},
		{"name address case-insensitive match", "Support@PayPal.com", "support@paypal.com", known, ""},
		{"name equals address", "x@evil.io", "x@evil.io", known, ""},
		{"name without address", "Jane Doe", "jane@unknown-company.org", known, ""},
		{"punycode domain", "Bank", "a@xn--pypal-4ve.com", nil, "look-alike characters"},
		{"punycode subdomain label", "Bank", "a@xn--abc.example.org", nil, "look-alike characters"},
		{"non-ascii domain", "Bank", "a@pаypal.com", nil, "look-alike characters"},
		{"plain ascii unknown domain", "Bank", "a@totally-new.org", known, ""},
		{"substitution of known domain", "", "a@paypa1.com", known, "looks like paypal.com"},
		{"deletion from known domain", "", "a@paypl.com", known, "looks like paypal.com"},
		{"insertion into known domain", "", "a@paypall.com", known, "looks like paypal.com"},
		{"swap in known domain", "", "a@paypla.com", known, "looks like paypal.com"},
		{"tld change", "", "a@paypal.co", known, "looks like paypal.com"},
		{"gmail typo flags", "", "a@gmai.com", known, "looks like gmail.com"},
		{"exact known domain", "", "a@paypal.com", known, ""},
		{"subdomain of known domain", "", "a@mail.paypal.com", known, ""},
		{"gmx vs gmail is two edits", "", "a@gmx.com", known, ""},
		{"short domains skipped", "", "a@b.io", map[string]int{"a.io": 9}, ""},
		{"distance two", "", "a@paypxx.com", known, ""},
		{"known domain with less history than sender", "", "a@paypa1.com", map[string]int{"paypal.com": 1, "paypa1.com": 5}, ""},
		{"no known domains", "", "a@paypa1.com", nil, ""},
		{"no at sign", "", "nobody", known, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := senderWarnings(tt.fromName, tt.fromEmail, tt.known)
			if tt.want == "" {
				if len(got) != 0 {
					t.Fatalf("senderWarnings() = %v, want none", got)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0], tt.want) {
				t.Fatalf("senderWarnings() = %v, want one containing %q", got, tt.want)
			}
		})
	}
}

func TestEmailPartialShowsSenderWarningOnlyForLookalike(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	if err := db.UpsertFolders(t.Context(), []storage.UpsertFolderInput{
		{ID: "victim-inbox", AccountID: "victim-account", RemoteID: "INBOX", Name: "Inbox", Role: "inbox", Selectable: true},
	}); err != nil {
		t.Fatalf("UpsertFolders() error = %v", err)
	}
	if err := db.UpsertSyncMessages(t.Context(), []storage.SyncMessage{
		{AccountID: "victim-account", FolderID: "victim-inbox", RemoteUID: 1, MessageID: "<spoof@example.com>", Subject: "Spoof", FromName: "support@paypal.com", FromEmail: "x@evil.io", DateSent: time.Now()},
		{AccountID: "victim-account", FolderID: "victim-inbox", RemoteUID: 2, MessageID: "<lookalike@example.com>", Subject: "Lookalike", FromName: "PayPal", FromEmail: "service@paypa1.com", DateSent: time.Now()},
		{AccountID: "victim-account", FolderID: "victim-inbox", RemoteUID: 3, MessageID: "<normal@example.com>", Subject: "Normal", FromName: "Pat", FromEmail: "pat@paypal.com", DateSent: time.Now()},
	}); err != nil {
		t.Fatalf("UpsertSyncMessages() error = %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := db.UpsertObservedContact(t.Context(), "owner", "Pat", "pat@paypal.com", time.Now()); err != nil {
			t.Fatalf("UpsertObservedContact() error = %v", err)
		}
	}
	if err := db.UpsertObservedContact(t.Context(), "owner", "PayPal", "service@paypa1.com", time.Now()); err != nil {
		t.Fatalf("UpsertObservedContact() error = %v", err)
	}

	render := func(internetID string) string {
		t.Helper()
		id, err := db.GetMessageLocalIDByInternetIDInternal(t.Context(), "victim-account", internetID)
		if err != nil || id == 0 {
			t.Fatalf("GetMessageLocalIDByInternetIDInternal(%s) = %d, %v", internetID, id, err)
		}
		req := httptest.NewRequest(http.MethodGet, "/email/"+strconv.FormatInt(id, 10)+"?folder_id=victim-inbox", nil)
		req.SetPathValue("id", strconv.FormatInt(id, 10))
		req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{ID: "owner", Username: "owner"}))
		recorder := httptest.NewRecorder()
		h.handleEmailPartial(recorder, req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		return recorder.Body.String()
	}

	spoof := render("<spoof@example.com>")
	if !strings.Contains(spoof, "data-sender-warning-banner") || !strings.Contains(spoof, "support@paypal.com") {
		t.Fatalf("spoofed message missing sender warning banner: %s", spoof)
	}
	lookalike := render("<lookalike@example.com>")
	if !strings.Contains(lookalike, "data-sender-warning-banner") || !strings.Contains(lookalike, "looks like paypal.com") {
		t.Fatalf("look-alike domain message missing sender warning banner: %s", lookalike)
	}
	if normal := render("<normal@example.com>"); strings.Contains(normal, "data-sender-warning-banner") {
		t.Fatalf("normal message shows sender warning banner: %s", normal)
	}
}
