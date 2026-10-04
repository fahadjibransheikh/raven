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

func microsoftAccountTokenScopes() []string { return microsoftAccountTokenExchangeScopes() }

func microsoftAccountTokenExchangeScopes() []string {
	return []string{"openid", "email", "profile", "offline_access", microsoftGraphContactsScope, microsoftGraphMailScope, microsoftGraphMailSendScope, microsoftGraphMailboxSettingsScope}
}

func (m *Service) accountOAuthConfig() *oauth2.Config {
	cfg := m.config.GoogleClient
	return &oauth2.Config{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, RedirectURL: m.config.BaseURL + "/auth/google/mailbox/callback", Scopes: cfg.Scopes, Endpoint: cfg.Endpoint}
}

func (m *Service) microsoftAccountOAuthConfig() *oauth2.Config {
	cfg := m.config.MicrosoftClient
	return &oauth2.Config{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, RedirectURL: m.config.BaseURL + "/auth/microsoft/mailbox/callback", Scopes: cfg.Scopes, Endpoint: cfg.Endpoint}
}
