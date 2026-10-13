package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fahadjibransheikh/raven/internal/config"
	"github.com/fahadjibransheikh/raven/internal/mail/imap"
	smtpclient "github.com/fahadjibransheikh/raven/internal/mail/smtp"
	mailtransport "github.com/fahadjibransheikh/raven/internal/mail/transport"
	"github.com/fahadjibransheikh/raven/internal/models"
	"github.com/fahadjibransheikh/raven/internal/storage"
	"github.com/fahadjibransheikh/raven/internal/views"
)

// accountConnectionTestResultHeader tells the add-account wizard whether every
// service connected, so it creates the account only after a passing test.
const accountConnectionTestResultHeader = "X-Raven-Connection-Test"

// handleTestNewAccount tests the IMAP and SMTP settings typed into the
// add-account wizard before the account exists. Nothing is saved: the
// password is used for this test only. The servers go through the same
// private-network and unencrypted-connection rules as a saved account.
func (h *Handler) handleTestNewAccount(w http.ResponseWriter, r *http.Request) {
	req, ok := parseTypedAccountTest(w, r)
	if !ok {
		return
	}
	if req.Password == "" {
		http.Error(w, "all required fields must be filled in", http.StatusBadRequest)
		return
	}
	h.writeTypedAccountTest(w, r, req)
}

// handleTestAccountChanges tests the settings typed into the edit-account
// wizard before they are saved. A password left blank keeps the saved one, so
// the test uses it too. Nothing is saved.
func (h *Handler) handleTestAccountChanges(w http.ResponseWriter, r *http.Request) {
	req, ok := parseTypedAccountTest(w, r)
	if !ok {
		return
	}
	password, smtpPassword, found, err := h.savedAccountPasswords(r, r.PathValue("id"))
	switch {
	case err != nil:
		userAccountError(w, r, err)
		return
	case !found:
		http.NotFound(w, r)
		return
	}
	if req.Password == "" {
		req.Password = password
	}
	if req.SmtpUsername != "" && req.SmtpPassword == "" {
		req.SmtpPassword = smtpPassword
	}
	h.writeTypedAccountTest(w, r, req)
}

func parseTypedAccountTest(w http.ResponseWriter, r *http.Request) (models.CreateAccountRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return models.CreateAccountRequest{}, false
	}
	req := accountRequestFromForm(r)
	if req.EmailAddress == "" || req.IMAPHost == "" || req.SMTPHost == "" || req.Username == "" {
		http.Error(w, "all required fields must be filled in", http.StatusBadRequest)
		return req, false
	}
	// OAuth mailboxes connect through the provider's authorization flow instead.
	if (req.Provider != "" && req.Provider != "imap") || (req.AuthMethod != "" && req.AuthMethod != "plain") {
		http.Error(w, "this account connection method can't be tested here", http.StatusBadRequest)
		return req, false
	}
	return req, true
}

// savedAccountPasswords reads the saved passwords of an account the current
// person owns and that signs in with a password.
func (h *Handler) savedAccountPasswords(r *http.Request, id string) (password, smtpPassword string, found bool, err error) {
	read := func(store *config.AccountStore) error {
		data, err := store.GetEditData(r.Context(), id)
		if err != nil || data == nil || data.Provider != "imap" || data.AuthMethod != "plain" {
			return err
		}
		if password, err = store.DecryptPassword(r.Context(), id); err != nil {
			return err
		}
		if data.SmtpUsername != "" {
			if smtpPassword, err = store.DecryptSmtpPassword(r.Context(), id); err != nil {
				return err
			}
		}
		found = true
		return nil
	}
	if h.userAccounts != nil {
		err = h.userAccounts.WithAccountForUser(r.Context(), h.userID(r.Context()), id, func(store *config.AccountStore, _ *storage.DB) error {
			return read(store)
		})
		return password, smtpPassword, found, err
	}
	if account, ownErr := h.ownedAccount(r.Context(), id); ownErr != nil || account == nil {
		return "", "", false, nil
	}
	err = read(h.accountStore)
	return password, smtpPassword, found, err
}

func (h *Handler) writeTypedAccountTest(w http.ResponseWriter, r *http.Request, req models.CreateAccountRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	cfg := &models.AccountConfig{
		Provider: "imap", AuthMethod: "plain", Username: req.Username, SmtpUsername: req.SmtpUsername,
		IMAPHost: req.IMAPHost, IMAPPort: req.IMAPPort,
		SMTPHost: req.SMTPHost, SMTPPort: req.SMTPPort,
	}
	smtpPassword := req.Password
	if req.SmtpUsername != "" {
		smtpPassword = req.SmtpPassword
	}
	results := []models.ConnectionTestResult{
		h.testNewAccountService(ctx, "imap", cfg, req.IMAPTLSMode, req.Password),
		h.testNewAccountService(ctx, "smtp", cfg, req.SMTPTLSMode, smtpPassword),
	}
	writeAccountTestResults(w, r, results)
}

// writeAccountTestResults renders connection test results for the account
// wizards, with a header saying whether every service connected.
func writeAccountTestResults(w http.ResponseWriter, r *http.Request, results []models.ConnectionTestResult) {
	outcome := "ok"
	for _, result := range results {
		if !result.Success {
			outcome = "failed"
		}
	}
	w.Header().Set(accountConnectionTestResultHeader, outcome)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = views.ConnectionTestResults(results, "", "add").Render(r.Context(), w)
}

func (h *Handler) testNewAccountService(ctx context.Context, protocol string, cfg *models.AccountConfig, tlsMode, password string) models.ConnectionTestResult {
	host, port := cfg.IMAPHost, cfg.IMAPPort
	if protocol == "smtp" {
		host, port = cfg.SMTPHost, cfg.SMTPPort
	}
	result := models.ConnectionTestResult{Service: protocol, Message: fmt.Sprintf("%s:%d (%s)", host, port, tlsMode)}
	err := h.prepareNewAccountTransport(ctx, protocol, cfg, tlsMode)
	if err == nil {
		err = runAccountConnectionTest(ctx, accountConnectionTestRetryDelay, func() error {
			if protocol == "smtp" {
				return smtpclient.TestConnection(ctx, cfg, password)
			}
			return imap.TestConnection(ctx, cfg, password)
		})
	}
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Success = true
	result.Message = "Connection successful"
	return result
}

// prepareNewAccountTransport applies the instance's mail security exceptions
// to one service, exactly as they apply when a saved account connects.
func (h *Handler) prepareNewAccountTransport(ctx context.Context, protocol string, cfg *models.AccountConfig, tlsMode string) error {
	host, port := cfg.IMAPHost, cfg.IMAPPort
	if protocol == "smtp" {
		host, port = cfg.SMTPHost, cfg.SMTPPort
	}
	allowPlaintext, err := h.db.IsPlaintextTransportAllowed(ctx, protocol, host, port)
	if err != nil {
		return fmt.Errorf("check %s security exception: %w", strings.ToUpper(protocol), err)
	}
	mode, err := mailtransport.RequireTLSModeWithPlaintext(protocol, tlsMode, allowPlaintext)
	if err != nil {
		return err
	}
	allowPrivate, err := h.db.IsPrivateTargetAllowed(ctx, protocol, host, port)
	if err != nil {
		return fmt.Errorf("check %s private target exception: %w", strings.ToUpper(protocol), err)
	}
	if protocol == "smtp" {
		cfg.SMTPTLSMode, cfg.SMTPAllowPlaintext, cfg.SMTPAllowPrivate = mode, allowPlaintext, allowPrivate
	} else {
		cfg.IMAPTLSMode, cfg.IMAPAllowPlaintext, cfg.IMAPAllowPrivate = mode, allowPlaintext, allowPrivate
	}
	return nil
}
