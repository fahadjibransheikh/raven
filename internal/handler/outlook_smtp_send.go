package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/mail/message"
	smtpclient "github.com/cristianadrielbraun/gofer/internal/mail/smtp"
	mailtransport "github.com/cristianadrielbraun/gofer/internal/mail/transport"
	"github.com/cristianadrielbraun/gofer/internal/mailauth"
	"github.com/cristianadrielbraun/gofer/internal/models"
	"github.com/cristianadrielbraun/gofer/internal/storage"
)

// Graph /me/sendMail ignores a send-as alias in From and delivers from the
// primary address, so alias sends go through Microsoft SMTP submission with
// OAuth (XOAUTH2) instead. Primary-address sends stay on Graph.
// https://learn.microsoft.com/en-us/exchange/client-developer/legacy-protocols/how-to-authenticate-an-imap-pop-smtp-application-by-using-oauth

const outlookReconnectForAliasesText = "Reconnect your Outlook account to send from aliases"

// outlookSMTPSend is a seam so tests can observe the SMTP config and token.
var outlookSMTPSend = smtpclient.SendRawMessageWithTiming

// outlookSendNeedsSMTP reports whether the From differs from the account's
// login address, i.e. the user picked an alias.
func (h *Handler) outlookSendNeedsSMTP(ctx context.Context, accountID string, msg *message.OutgoingMessage) bool {
	from := strings.TrimSpace(msg.FromEmail)
	primary := strings.TrimSpace(h.outgoingEnvelopeFrom(ctx, accountID, msg))
	return from != "" && primary != "" && !strings.EqualFold(from, primary)
}

// outlookSMTPServer returns the submission host: smtp-mail.outlook.com for
// personal Microsoft accounts, smtp.office365.com for Microsoft 365 work
// accounts. Port 587 with STARTTLS for both.
// https://support.microsoft.com/en-us/office/pop-imap-and-smtp-settings-for-outlook-com-d088b986-291d-42b8-9564-9c414e2aa040
// https://learn.microsoft.com/en-us/exchange/mail-flow-best-practices/how-to-set-up-a-multifunction-device-or-application-to-send-email-using-microsoft-365-or-office-365
func outlookSMTPServer(accountEmail string) string {
	// ponytail: guessed from the mailbox domain because Raven doesn't store the
	// tenant; a custom domain on a personal account would need an override.
	if _, domain, ok := strings.Cut(strings.ToLower(accountEmail), "@"); ok {
		switch strings.SplitN(domain, ".", 2)[0] {
		case "outlook", "hotmail", "live", "msn", "passport":
			return "smtp-mail.outlook.com"
		}
	}
	return "smtp.office365.com"
}

func (h *Handler) sendOutlookSMTPRaw(ctx context.Context, cfg *models.AccountConfig, send storage.OutgoingSend, msg *message.OutgoingMessage) error {
	if h.mailCredentials() == nil {
		return fmt.Errorf("microsoft oauth not configured")
	}
	token, err := h.mailCredentials().GetMicrosoftSMTPTokenForAccount(ctx, cfg.AccountID)
	if err != nil {
		switch {
		case errors.Is(err, mailauth.ErrMicrosoftSMTPConsentRequired):
			// Never fall back to Graph: it would send from the wrong address.
			return &outgoingReconnectError{message: outlookReconnectForAliasesText}
		case isPermanentOAuthError(err):
			return markOutgoingSendReconnect(err)
		}
		return markOutgoingSendRetryable(err)
	}

	// The queued MIME is the Graph flavor, which keeps Bcc. Rebuild without it.
	raw, err := message.BuildMIMEMessage(msg)
	if err != nil {
		return fmt.Errorf("build outgoing message: %w", err)
	}
	login := send.EnvelopeFrom
	smtpCfg := *cfg
	smtpCfg.SMTPHost, smtpCfg.SMTPPort = outlookSMTPServer(login), 587
	smtpCfg.SMTPTLSMode, smtpCfg.SMTPAllowPlaintext = mailtransport.TLSModeStartTLS, false
	smtpCfg.AuthMethod, smtpCfg.Username, smtpCfg.SmtpUsername = "oauth2", login, login

	return h.runSMTPSendFrom(ctx, time.Now(), &smtpCfg, token, send, raw, outlookSMTPSend)
}

// cacheOutlookSMTPSentMessageID links the local Sent record to the copy
// Exchange saves on SMTP submission (client SMTP submission "Saves to Sent
// Items: Yes" per Microsoft's SMTP AUTH documentation). Best effort.
func (h *Handler) cacheOutlookSMTPSentMessageID(ctx context.Context, accountID string, msg *message.OutgoingMessage) {
	token, err := h.mailCredentials().GetMicrosoftGraphMailTokenForAccount(ctx, accountID)
	if err != nil {
		log.Printf("outlook smtp sent reconcile token account=%s: %v", accountID, err)
		return
	}
	h.cacheOutlookSentMessageID(ctx, accountID, msg, token)
}
