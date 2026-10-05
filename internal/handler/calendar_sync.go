package handler

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/providers"
)

var errCalendarSyncAlreadyRunning = errors.New("calendar sync already running for this account")

// StartCalendarSync mirrors StartContactSync: one sync at startup, then one
// per user sync interval. Failures are logged and never fatal.
func (h *Handler) StartCalendarSync(ctx context.Context) {
	go func() {
		h.SyncCalendarsForAllAccounts(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(h.contactSyncInterval(ctx)): // same cadence as contacts
				h.SyncCalendarsForAllAccounts(ctx)
			}
		}
	}()
}

func (h *Handler) SyncCalendarsForAllAccounts(ctx context.Context) {
	accounts, err := h.db.GetAllAccountIDs(ctx)
	if err != nil {
		log.Printf("calendar sync: list accounts: %v", err)
		return
	}
	for _, accountID := range accounts {
		if ctx.Err() != nil {
			return
		}
		if err := h.SyncCalendarAccount(ctx, accountID); err != nil && !errors.Is(err, errCalendarSyncAlreadyRunning) {
			log.Printf("calendar sync %s: %v", accountID, err)
		}
	}
}

// SyncCalendarAccount syncs one account; accounts whose provider has no
// calendar support are skipped with a nil error.
func (h *Handler) SyncCalendarAccount(ctx context.Context, accountID string) error {
	var provider string
	if err := h.db.Read().QueryRowContext(ctx, `SELECT provider FROM accounts WHERE id = ? AND COALESCE(is_deleting, 0) = 0`, accountID).Scan(&provider); err != nil {
		return nil // missing or deleting account
	}
	var pull func(context.Context, string) error
	var isScopeErr func(error) bool
	reconnectMsg := ""
	switch {
	case provider == providers.ProviderGmail && h.mailCredentials() != nil && h.mailCredentials().HasGoogleOAuth():
		pull, isScopeErr, reconnectMsg = h.pullGoogleCalendarAccount, isGoogleCalendarScopeError, "reconnect Google to grant calendar access"
	case provider == providers.ProviderOutlook && h.mailCredentials() != nil && h.mailCredentials().HasMicrosoftOAuth():
		pull, isScopeErr, reconnectMsg = h.pullOutlookCalendarAccount, isOutlookCalendarScopeError, "reconnect Outlook to grant calendar access"
	default:
		return nil
	}
	if !h.beginCalendarSync(accountID) {
		return errCalendarSyncAlreadyRunning
	}
	defer h.endCalendarSync(accountID)

	err := pull(ctx, accountID)
	switch {
	case err == nil:
		err = h.db.SetCalendarAccountState(ctx, accountID, false, "")
	case isScopeErr(err):
		if e := h.db.SetCalendarAccountState(ctx, accountID, true, reconnectMsg); e != nil {
			log.Printf("calendar sync %s: record reconnect state: %v", accountID, e)
		}
	default:
		if e := h.db.SetCalendarAccountState(ctx, accountID, false, err.Error()); e != nil {
			log.Printf("calendar sync %s: record error: %v", accountID, e)
		}
	}
	return err
}

func (h *Handler) pullGoogleCalendarAccount(ctx context.Context, accountID string) error {
	token, err := h.mailCredentials().GetOAuthTokenForAccount(ctx, accountID)
	if err != nil {
		return err
	}
	calendars, err := h.fetchGoogleCalendarList(ctx, token)
	if err != nil {
		return err
	}
	if err := h.db.UpsertCalendars(ctx, accountID, calendars); err != nil {
		return err
	}
	selected, err := h.db.ListSelectedCalendarsForAccount(ctx, accountID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, cal := range selected {
		if err := h.syncGoogleCalendarEvents(ctx, token, cal); err != nil {
			if isGoogleCalendarScopeError(err) {
				return err
			}
			log.Printf("calendar sync %s calendar %d: %v", accountID, cal.ID, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (h *Handler) beginCalendarSync(accountID string) bool {
	h.calendarSyncMu.Lock()
	defer h.calendarSyncMu.Unlock()
	if h.calendarSyncRunning == nil {
		h.calendarSyncRunning = make(map[string]struct{})
	}
	if _, ok := h.calendarSyncRunning[accountID]; ok {
		return false
	}
	h.calendarSyncRunning[accountID] = struct{}{}
	return true
}

func (h *Handler) endCalendarSync(accountID string) {
	h.calendarSyncMu.Lock()
	delete(h.calendarSyncRunning, accountID)
	h.calendarSyncMu.Unlock()
}
