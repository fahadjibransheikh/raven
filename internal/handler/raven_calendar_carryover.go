package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/fahadjibransheikh/raven/internal/providers"
	"github.com/fahadjibransheikh/raven/internal/storage"
)

// ravenCalendarCarryoverRetry spaces discovery attempts for accounts whose
// tokens or servers are not ready yet, so a failing account is not retried on
// every 30-second calendar tick.
const ravenCalendarCarryoverRetry = 10 * time.Minute

// runRavenCalendarCarryoverTick discovers calendars for accounts that had
// calendars in Raven's old calendar, so the selection kept in
// raven_calendar_carryover is applied without the user opening account setup.
// Successful discovery clears the carryover (storage.ReplaceCalendarSources).
func (h *Handler) runRavenCalendarCarryoverTick(ctx context.Context, now time.Time) {
	if !h.ravenCarryoverNextAttempt.IsZero() && now.Before(h.ravenCarryoverNextAttempt) {
		return
	}
	accounts, err := h.db.RavenCalendarCarryoverAccounts(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("calendar carryover: list accounts: %v", err)
		}
		return
	}
	if len(accounts) == 0 {
		h.ravenCarryoverNextAttempt = now.Add(24 * time.Hour)
		return
	}
	h.ravenCarryoverNextAttempt = now.Add(ravenCalendarCarryoverRetry)
	for _, account := range accounts {
		if ctx.Err() != nil {
			return
		}
		count, err := h.discoverRavenCarryoverAccount(ctx, account)
		if err != nil {
			log.Printf("calendar carryover: account %s (%s): %v", account.AccountID, account.Provider, err)
			continue
		}
		log.Printf("calendar carryover: account %s (%s): discovered %d calendar(s), restored Raven selection", account.AccountID, account.Provider, count)
	}
}

func (h *Handler) discoverRavenCarryoverAccount(ctx context.Context, account storage.RavenCalendarCarryoverAccount) (int, error) {
	switch account.Provider {
	case providers.ProviderGmail:
		if h.mailCredentials() == nil || !h.mailCredentials().HasGoogleOAuth() {
			return 0, errors.New("google OAuth is not configured")
		}
		token, err := h.mailCredentials().GetGoogleCalendarTokenForAccount(ctx, account.AccountID)
		if err != nil {
			return 0, err
		}
		return h.discoverGoogleCalendarSources(ctx, account.UserID, account.AccountID, token)
	case providers.ProviderOutlook:
		if h.mailCredentials() == nil || !h.mailCredentials().HasMicrosoftOAuth() {
			return 0, errors.New("microsoft OAuth is not configured")
		}
		token, err := h.mailCredentials().GetMicrosoftGraphCalendarTokenForAccount(ctx, account.AccountID)
		if err != nil {
			return 0, err
		}
		return h.discoverOutlookCalendarSources(ctx, account.UserID, account.AccountID, token)
	case providers.ProviderIMAP:
		return h.discoverRavenCarryoverCalDAV(ctx, account)
	default:
		return 0, fmt.Errorf("calendar discovery is not available for provider %q", account.Provider)
	}
}

// discoverRavenCarryoverCalDAV mirrors account setup's CalDAV autodiscovery
// with the incoming-mail credentials, which is how Raven's old calendar
// reached iCloud.
func (h *Handler) discoverRavenCarryoverCalDAV(ctx context.Context, account storage.RavenCalendarCarryoverAccount) (int, error) {
	data, err := h.accountStore.GetEditData(ctx, account.AccountID)
	if err != nil {
		return 0, fmt.Errorf("load account: %w", err)
	}
	username := strings.TrimSpace(data.Username)
	if username == "" {
		username = strings.TrimSpace(data.EmailAddress)
	}
	password, err := h.accountStore.DecryptPassword(ctx, account.AccountID)
	if err != nil || strings.TrimSpace(password) == "" {
		return 0, errors.New("mailbox password is not available")
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, cardDAVDiscoveryOverallTimeout)
	defer cancel()
	discoveryCtx = withCalDAVAnchor(discoveryCtx, data.CalendarSync.CalDAVBaseURL) // the saved server only
	candidates := calDAVAutodiscoveryCandidates(discoveryCtx, data.CalendarSync.CalDAVBaseURL, data.EmailAddress, data.IMAPHost, data.SMTPHost)
	calendars, baseURL, err := discoverCalDAVCalendarsCandidates(discoveryCtx, candidates, username, password, account.UserID, account.AccountID)
	if err != nil {
		return 0, err
	}
	if err := h.accountStore.SaveCalDAVConfig(ctx, account.UserID, account.AccountID, baseURL, username, password, true); err != nil {
		return 0, fmt.Errorf("save CalDAV settings: %w", err)
	}
	if err := h.db.ReplaceCalendarSources(ctx, account.UserID, account.AccountID, storage.CalendarSourceProviderCalDAV, calendars); err != nil {
		return 0, err
	}
	return len(calendars), nil
}
