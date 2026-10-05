package mailauth

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/oauth2"
)

func googleAccountScopes() []string {
	return []string{
		"openid",
		"email",
		"profile",
		"https://mail.google.com/",
		"https://www.googleapis.com/auth/contacts",
		// Narrowest pair that lists calendars and reads/writes events; the full
		// "calendar" scope would also allow editing calendar settings and sharing.
		"https://www.googleapis.com/auth/calendar.calendarlist.readonly",
		"https://www.googleapis.com/auth/calendar.events",
	}
}

func (m *Service) GoogleAccountOAuthURL(state string) string {
	return m.accountOAuthConfig().AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
}

// MicrosoftAccountOAuthURL starts the Outlook mailbox flow with a PKCE
// challenge for verifier, which must be passed back to
// ExchangeMicrosoftAccountCode. PKCE is what protects the code exchange when
// Raven runs as a public client without a secret.
func (m *Service) MicrosoftAccountOAuthURL(state, verifier string) string {
	return m.microsoftAccountOAuthConfig().AuthCodeURL(state, oauth2.SetAuthURLParam("prompt", "consent"), oauth2.S256ChallengeOption(verifier))
}

func (m *Service) ExchangeAccountCode(ctx context.Context, code string) (*oauth2.Token, error) {
	token, err := m.accountOAuthConfig().Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("oauth exchange: %w", err)
	}
	return token, nil
}

func (m *Service) ExchangeMicrosoftAccountCode(ctx context.Context, code, verifier string) (*oauth2.Token, error) {
	opts := []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("scope", strings.Join(microsoftAccountTokenExchangeScopes(), " "))}
	if verifier != "" {
		opts = append(opts, oauth2.VerifierOption(verifier))
	}
	token, err := m.microsoftAccountOAuthConfig().Exchange(ctx, code, opts...)
	if err != nil {
		return nil, fmt.Errorf("oauth exchange: %w", err)
	}
	return token, nil
}

// microsoftAccountTokenScopes is what the authorize leg asks the user to
// consent to. It adds the Outlook SMTP scope, which is a different resource
// than Graph, so it can be consented here but must stay out of the code
// exchange (a token request may name only one resource).
func microsoftAccountTokenScopes() []string {
	return append(microsoftAccountTokenExchangeScopes(), microsoftSMTPSendScope)
}

func microsoftAccountTokenExchangeScopes() []string {
	return []string{"openid", "email", "profile", "offline_access", microsoftGraphContactsScope, microsoftGraphMailScope, microsoftGraphMailSendScope, microsoftGraphMailboxSettingsScope, microsoftGraphCalendarScope}
}

func (m *Service) accountOAuthConfig() *oauth2.Config {
	cfg := m.config.GoogleClient
	return &oauth2.Config{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, RedirectURL: m.config.BaseURL + "/auth/google/mailbox/callback", Scopes: cfg.Scopes, Endpoint: cfg.Endpoint}
}

func (m *Service) microsoftAccountOAuthConfig() *oauth2.Config {
	cfg := m.config.MicrosoftClient
	return &oauth2.Config{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, RedirectURL: m.config.BaseURL + "/auth/microsoft/mailbox/callback", Scopes: cfg.Scopes, Endpoint: cfg.Endpoint}
}
