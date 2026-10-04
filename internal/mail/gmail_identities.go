package mail

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/storage"
)

// Gmail send-as aliases change rarely; the regular sync re-reads them at most
// this often. The manual Refresh action ignores the throttle.
const gmailIdentityRefreshInterval = 24 * time.Hour

// gmailSendAs is the subset of the users.settings.sendAs resource Raven uses.
// https://developers.google.com/workspace/gmail/api/reference/rest/v1/users.settings.sendAs
type gmailSendAs struct {
	SendAsEmail        string `json:"sendAsEmail"`
	DisplayName        string `json:"displayName"`
	IsPrimary          bool   `json:"isPrimary"`
	IsDefault          bool   `json:"isDefault"`
	VerificationStatus string `json:"verificationStatus"`
}

type gmailSendAsListResponse struct {
	SendAs []gmailSendAs `json:"sendAs"`
}

// RefreshGmailIdentities fetches the account's Gmail send-as aliases now and
// reconciles them into its identities, regardless of the 24h throttle.
func (o *SyncOrchestrator) RefreshGmailIdentities(ctx context.Context, accountID string) error {
	if o == nil || o.tokenProvider == nil {
		return errProviderLabelAuth
	}
	token, err := o.tokenProvider.GetOAuthTokenForAccount(ctx, accountID)
	if err != nil {
		return err
	}
	var response gmailSendAsListResponse
	endpoint := gmailAPIBaseURL + "/users/me/settings/sendAs"
	err = providerJSON(ctx, http.MethodGet, endpoint, token, nil, nil, &response)
	var apiErr *providerAPIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
		if token, rerr := o.refreshOAuthTokenForAccount(ctx, accountID); rerr == nil {
			err = providerJSON(ctx, http.MethodGet, endpoint, token, nil, nil, &response)
		}
	}
	now := time.Now()
	if err != nil {
		// Stamp the attempt so a failing account is retried daily, not every sync.
		if serr := o.db.TouchIdentitiesSyncedAt(ctx, accountID, now); serr != nil {
			log.Printf("gmail identities %s: record attempt: %v", accountID, serr)
		}
		return err
	}

	var aliases []storage.ProviderIdentity
	providerDefault := ""
	for _, sa := range response.SendAs {
		// Only usable aliases: the primary (already an identity) or accepted ones.
		if !sa.IsPrimary && sa.VerificationStatus != "accepted" {
			continue
		}
		email := strings.TrimSpace(sa.SendAsEmail)
		if email == "" {
			continue
		}
		if !sa.IsPrimary {
			aliases = append(aliases, storage.ProviderIdentity{Email: email, Name: sa.DisplayName})
		}
		if sa.IsDefault {
			providerDefault = email
		}
	}
	return o.db.ApplyProviderIdentities(ctx, accountID, aliases, providerDefault, now)
}

// refreshGmailIdentitiesIfDue is the sync-path entry point. Failures are logged
// and never fail the sync.
func (o *SyncOrchestrator) refreshGmailIdentitiesIfDue(ctx context.Context, accountID string) {
	at, ok, err := o.db.IdentitiesSyncedAt(ctx, accountID)
	if err != nil {
		log.Printf("gmail identities %s: read last fetch: %v", accountID, err)
		return
	}
	if ok && time.Since(at) < gmailIdentityRefreshInterval {
		return
	}
	if err := o.RefreshGmailIdentities(ctx, accountID); err != nil {
		log.Printf("gmail identities %s: %v", accountID, err)
	}
}
