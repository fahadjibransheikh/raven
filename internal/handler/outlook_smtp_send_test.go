package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/cristianadrielbraun/gofer/internal/auth"
	"github.com/cristianadrielbraun/gofer/internal/config"
	mailpkg "github.com/cristianadrielbraun/gofer/internal/mail"
	"github.com/cristianadrielbraun/gofer/internal/mail/message"
	smtpclient "github.com/cristianadrielbraun/gofer/internal/mail/smtp"
	"github.com/cristianadrielbraun/gofer/internal/mailauth"
	"github.com/cristianadrielbraun/gofer/internal/models"
	"github.com/cristianadrielbraun/gofer/internal/providers"
	"github.com/cristianadrielbraun/gofer/internal/storage"
	"github.com/cristianadrielbraun/gofer/internal/store"
)

type outlookSMTPFixture struct {
	h          *Handler
	db         *storage.DB
	graphSends int
	reconciles int
	smtpScope  func(w http.ResponseWriter) // writes the token response for the SMTP scope
	smtpCalls  []smtpCall
	smtpResult models.SendResult
	smtpErr    error
}

type smtpCall struct {
	cfg        models.AccountConfig
	secret     string
	from       string
	recipients []string
	mime       string
}

func newOutlookSMTPFixture(t *testing.T) *outlookSMTPFixture {
	t.Helper()
	ctx := context.Background()
	f := &outlookSMTPFixture{smtpResult: models.SendSuccess}
	db, err := storage.New(filepath.Join(t.TempDir(), "gofer.db"))
	if err != nil {
		t.Fatalf("storage.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f.db = db
	if _, err := db.Write().ExecContext(ctx, `INSERT INTO users (id, username, username_normalized, name) VALUES ('default', 'default', 'default', 'Default')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Write().ExecContext(ctx, `
		INSERT INTO accounts (id, user_id, provider, provider_account_id, email_address)
		VALUES ('acc', 'default', ?, 'subject', 'fahadsheikh@msn.com')`, providers.ProviderOutlook); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if err := db.UpsertFolders(ctx, []storage.UpsertFolderInput{{ID: "acc_sent", AccountID: "acc", RemoteID: "Sent Items", ProviderRemoteID: "graph-sent", Name: "Sent", Role: "sent", Selectable: true}}); err != nil {
		t.Fatalf("UpsertFolders() error = %v", err)
	}
	expires := time.Now().Add(time.Hour)
	seed := mailauth.New(&mailauth.Config{}, db, testMailboxCredentialKey)
	if err := seed.UpsertOAuthAccount(ctx, "acc", providers.OAuthMicrosoft, "subject", "stale", "refresh-token", "Bearer", &expires, ""); err != nil {
		t.Fatalf("UpsertOAuthAccount() error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			_ = r.ParseForm()
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(r.FormValue("scope"), "outlook.office.com/SMTP.Send") {
				f.smtpScope(w)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"graph-token","token_type":"Bearer","expires_in":3600}`))
		case r.Method == http.MethodPost && r.URL.Path == "/me/sendMail":
			f.graphSends++
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/me/messages":
			f.reconciles++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"value": []map[string]string{{"id": "graph-sent-1"}}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	f.smtpScope = func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"access_token":"smtp-token","token_type":"Bearer","expires_in":3600,"scope":"https://outlook.office.com/SMTP.Send"}`))
	}

	previousGraph, previousSend := outlookGraphBaseURL, outlookSMTPSend
	outlookGraphBaseURL = server.URL
	outlookSMTPSend = func(_ context.Context, cfg *models.AccountConfig, secret, from string, recipients []string, mime []byte) (models.SendResult, error, smtpclient.DeliveryTiming) {
		f.smtpCalls = append(f.smtpCalls, smtpCall{*cfg, secret, from, recipients, string(mime)})
		return f.smtpResult, f.smtpErr, smtpclient.DeliveryTiming{}
	}
	t.Cleanup(func() { outlookGraphBaseURL, outlookSMTPSend = previousGraph, previousSend })

	credentials := mailauth.New(&mailauth.Config{MicrosoftClient: &oauth2.Config{ClientID: "client", Endpoint: oauth2.Endpoint{TokenURL: server.URL + "/token"}}}, db, testMailboxCredentialKey)
	accountStore, err := config.NewAccountStore(db, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewAccountStore() error = %v", err)
	}
	f.h = New(db, accountStore, mailpkg.NewSyncOrchestrator(db, accountStore, nil, nil), store.NewBlobStore(filepath.Join(t.TempDir(), "blobs")), auth.NewManager(&auth.Config{}, db), "", credentials)
	return f
}

// send queues and delivers one message and returns the stored send.
func (f *outlookSMTPFixture) send(t *testing.T, from string) storage.OutgoingSend {
	t.Helper()
	ctx := context.Background()
	to, _ := message.ParseAddressList("recipient@example.com")
	bcc, _ := message.ParseAddressList("hidden@example.com")
	msg := &message.OutgoingMessage{
		FromEmail: from, FromName: "Fahad", To: to, Bcc: bcc, Subject: "Alias send", TextBody: "body",
		MessageID: "<alias@example.com>", Date: time.Now().UTC(),
	}
	queued, err := f.h.queueOutgoingMessage(ctx, "acc", 0, "", msg, time.Now().Add(-time.Second), false)
	if err != nil {
		t.Fatalf("queueOutgoingMessage() error = %v", err)
	}
	f.h.runDueOutgoingSends(ctx)
	done, err := f.db.GetOutgoingSend(ctx, queued.ID)
	if err != nil {
		t.Fatalf("GetOutgoingSend() error = %v", err)
	}
	return done
}

func TestOutlookAliasSendGoesThroughSMTPWithXOAUTH2Identity(t *testing.T) {
	f := newOutlookSMTPFixture(t)
	done := f.send(t, "fahadsheikh@outlook.com")

	if done.Status != storage.OutgoingSendSent {
		t.Fatalf("status = %q (%s), want sent", done.Status, done.LastError)
	}
	if f.graphSends != 0 {
		t.Fatalf("Graph sendMail called %d times; alias sends must use SMTP", f.graphSends)
	}
	if len(f.smtpCalls) != 1 {
		t.Fatalf("SMTP calls = %d, want 1", len(f.smtpCalls))
	}
	call := f.smtpCalls[0]
	if call.secret != "smtp-token" {
		t.Fatalf("SMTP secret = %q, want the SMTP.Send token", call.secret)
	}
	if call.cfg.AuthMethod != "oauth2" || call.cfg.SmtpUsername != "fahadsheikh@msn.com" {
		t.Fatalf("SMTP auth = %q as %q, want oauth2 as the account login", call.cfg.AuthMethod, call.cfg.SmtpUsername)
	}
	if call.cfg.SMTPHost != "smtp-mail.outlook.com" || call.cfg.SMTPPort != 587 || call.cfg.SMTPTLSMode != "starttls" {
		t.Fatalf("SMTP server = %s:%d %s, want smtp-mail.outlook.com:587 starttls", call.cfg.SMTPHost, call.cfg.SMTPPort, call.cfg.SMTPTLSMode)
	}
	if call.from != "fahadsheikh@msn.com" {
		t.Fatalf("envelope from = %q, want the account login", call.from)
	}
	if !strings.Contains(strings.Join(call.recipients, ","), "hidden@example.com") {
		t.Fatalf("recipients = %v, want Bcc included in the envelope", call.recipients)
	}
	if !strings.Contains(call.mime, "fahadsheikh@outlook.com") {
		t.Fatalf("MIME From does not carry the alias:\n%s", call.mime)
	}
	if strings.Contains(strings.ToLower(call.mime), "bcc:") {
		t.Fatalf("SMTP MIME leaks a Bcc header:\n%s", call.mime)
	}
	// Sent copy: Exchange saves it on SMTP submission; Raven records its own
	// and links it to the server copy through Graph.
	if id, err := f.db.GetMessageLocalIDByInternetIDInternal(context.Background(), "acc", "<alias@example.com>"); err != nil || id == 0 {
		t.Fatalf("local Sent record = %d, %v", id, err)
	}
	if f.reconciles != 1 {
		t.Fatalf("Graph Sent reconcile calls = %d, want 1", f.reconciles)
	}
}

func TestOutlookPrimarySendStaysOnGraph(t *testing.T) {
	f := newOutlookSMTPFixture(t)
	done := f.send(t, "FahadSheikh@MSN.com")

	if done.Status != storage.OutgoingSendSent {
		t.Fatalf("status = %q (%s), want sent", done.Status, done.LastError)
	}
	if f.graphSends != 1 || len(f.smtpCalls) != 0 {
		t.Fatalf("graph sends = %d, smtp calls = %d; want Graph only", f.graphSends, len(f.smtpCalls))
	}
}

func TestOutlookAliasSendWithoutSMTPConsentAsksToReconnectAndNeverUsesGraph(t *testing.T) {
	f := newOutlookSMTPFixture(t)
	f.smtpScope = func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"AADSTS65001: The user or administrator has not consented"}`))
	}
	done := f.send(t, "fahadsheikh@outlook.com")

	if done.Status != storage.OutgoingSendFailed || done.LastError != outlookReconnectForAliasesText {
		t.Fatalf("send = %q / %q, want failed with %q", done.Status, done.LastError, outlookReconnectForAliasesText)
	}
	if f.graphSends != 0 || len(f.smtpCalls) != 0 {
		t.Fatalf("graph sends = %d, smtp calls = %d; want neither", f.graphSends, len(f.smtpCalls))
	}
	if id, _ := f.db.GetMessageLocalIDByInternetIDInternal(context.Background(), "acc", "<alias@example.com>"); id != 0 {
		t.Fatal("a message that was not sent was recorded in Sent")
	}
}

func TestOutlookAliasSendDeniedBySMTPIsAFailureNotASentMessage(t *testing.T) {
	f := newOutlookSMTPFixture(t)
	f.smtpResult = models.SendFailed
	f.smtpErr = errors.New("close data: SMTP error 550: SMTP; Client does not have permissions to send as this sender")
	done := f.send(t, "fahadsheikh@outlook.com")

	if done.Status != storage.OutgoingSendFailed || !strings.Contains(done.LastError, "permissions to send as") {
		t.Fatalf("send = %q / %q, want a failure carrying the SMTP rejection", done.Status, done.LastError)
	}
	if f.graphSends != 0 {
		t.Fatal("a rejected alias send fell back to Graph")
	}
	if id, _ := f.db.GetMessageLocalIDByInternetIDInternal(context.Background(), "acc", "<alias@example.com>"); id != 0 {
		t.Fatal("a rejected send was recorded in Sent")
	}
}

func TestOutlookSMTPServerByAccountType(t *testing.T) {
	for email, want := range map[string]string{
		"a@outlook.com": "smtp-mail.outlook.com", "a@hotmail.co.uk": "smtp-mail.outlook.com",
		"a@live.com": "smtp-mail.outlook.com", "a@msn.com": "smtp-mail.outlook.com",
		"a@contoso.com": "smtp.office365.com", "a@contoso.onmicrosoft.com": "smtp.office365.com",
	} {
		if got := outlookSMTPServer(email); got != want {
			t.Errorf("outlookSMTPServer(%q) = %q, want %q", email, got, want)
		}
	}
}
