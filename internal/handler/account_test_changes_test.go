package handler

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

type passwordSMTPBackend struct{ user, password string }

func (b passwordSMTPBackend) NewSession(*smtp.Conn) (smtp.Session, error) {
	return &passwordSMTPSession{backend: b}, nil
}

type passwordSMTPSession struct{ backend passwordSMTPBackend }

func (s *passwordSMTPSession) AuthMechanisms() []string { return []string{sasl.Plain} }
func (s *passwordSMTPSession) Auth(string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(_, username, password string) error {
		if username != s.backend.user || password != s.backend.password {
			return errors.New("invalid credentials")
		}
		return nil
	}), nil
}
func (s *passwordSMTPSession) Mail(string, *smtp.MailOptions) error { return nil }
func (s *passwordSMTPSession) Rcpt(string, *smtp.RcptOptions) error { return nil }
func (s *passwordSMTPSession) Data(r io.Reader) error               { _, err := io.Copy(io.Discard, r); return err }
func (s *passwordSMTPSession) Reset()                               {}
func (s *passwordSMTPSession) Logout() error                        { return nil }

// startPasswordMailServers runs IMAP and SMTP servers that accept one login.
func startPasswordMailServers(t *testing.T, user, password string) (imapPort, smtpPort int) {
	t.Helper()
	mem := imapmemserver.New()
	mailbox := imapmemserver.NewUser(user, password)
	_ = mailbox.Create("INBOX", nil)
	mem.AddUser(mailbox)
	imapSrv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	imapLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = imapSrv.Serve(imapLn) }()
	t.Cleanup(func() { _ = imapSrv.Close() })

	smtpSrv := smtp.NewServer(passwordSMTPBackend{user: user, password: password})
	smtpSrv.Domain = "localhost"
	smtpSrv.AllowInsecureAuth = true
	smtpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = smtpSrv.Serve(smtpLn) }()
	t.Cleanup(func() { _ = smtpSrv.Close() })
	return imapLn.Addr().(*net.TCPAddr).Port, smtpLn.Addr().(*net.TCPAddr).Port
}

// The edit wizard tests typed changes before saving them. A password left
// blank keeps the saved one, so the test signs in with it; nothing is saved,
// and only the account's owner can test it.
func TestAccountChangesTestUsesTheSavedPasswordAndSavesNothing(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	imapPort, smtpPort := startPasswordMailServers(t, "lena@example.test", "saved-secret")
	for protocol, port := range map[string]int{"imap": imapPort, "smtp": smtpPort} {
		if err := db.AddPrivateTargetException(t.Context(), protocol, "127.0.0.1", port, "administrator"); err != nil {
			t.Fatal(err)
		}
		if err := db.AddPlaintextTransportException(t.Context(), protocol, "127.0.0.1", port, "administrator"); err != nil {
			t.Fatal(err)
		}
	}
	account, err := h.accountStore.CreateAccount(t.Context(), "owner", &models.CreateAccountRequest{
		Provider: "imap", AuthMethod: "plain", EmailAddress: "lena@example.test", DisplayName: "Lena",
		IMAPHost: "127.0.0.1", IMAPPort: imapPort, IMAPTLSMode: "plaintext",
		SMTPHost: "127.0.0.1", SMTPPort: smtpPort, SMTPTLSMode: "plaintext",
		Username: "lena@example.test", Password: "saved-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	form := func(password string) url.Values {
		return url.Values{
			"email_address": {"lena@example.test"}, "display_name": {"Lena R."}, "username": {"lena@example.test"}, "password": {password},
			"imap_host": {"127.0.0.1"}, "imap_port": {strconv.Itoa(imapPort)}, "imap_tls_mode": {"plaintext"},
			"smtp_host": {"127.0.0.1"}, "smtp_port": {strconv.Itoa(smtpPort)}, "smtp_tls_mode": {"plaintext"},
		}
	}
	test := func(asOwner bool, values url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+account.ID+"/test-changes", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("id", account.ID)
		if asOwner {
			req = ownerRequest(req)
		} else {
			req = attackerRequest(req)
		}
		rec := httptest.NewRecorder()
		h.handleTestAccountChanges(rec, req)
		return rec
	}

	if rec := test(true, form("")); rec.Header().Get(accountConnectionTestResultHeader) != "ok" {
		t.Fatalf("blank password test = %d %q %q", rec.Code, rec.Header().Get(accountConnectionTestResultHeader), rec.Body.String())
	}
	if rec := test(true, form("typed-but-wrong")); rec.Header().Get(accountConnectionTestResultHeader) != "failed" {
		t.Fatalf("wrong typed password test = %q", rec.Header().Get(accountConnectionTestResultHeader))
	}
	if rec := test(false, form("")); rec.Code != http.StatusNotFound {
		t.Fatalf("another person's account test = %d, want 404", rec.Code)
	}

	data, err := h.accountStore.GetEditData(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := h.accountStore.DecryptPassword(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if data.DisplayName != "Lena" || saved != "saved-secret" {
		t.Fatalf("testing changed the account: name %q, password changed %t", data.DisplayName, saved != "saved-secret")
	}
}
