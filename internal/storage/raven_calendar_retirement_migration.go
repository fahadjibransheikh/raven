package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Raven shipped its own calendar cache at v97 (calendars, calendar_events,
// calendar_account_state). Upstream gofer later built an incompatible calendar
// under the same calendar_events name; Raven now uses upstream's, created by
// migrateV107ToV108 onwards. See docs/upstream-merge-plan.md.

// migrateV96ToV97 used to create Raven's calendar tables. They are dropped again
// at v107, so a database still below v97 just records the version.
func migrateV96ToV97(tx *sql.Tx) error {
	return markSchemaVersion(tx, 97)
}

// ravenCalendarCarryoverTable keeps which calendars each account had in Raven's
// calendar, so the first calendar discovery after the upgrade can select them
// again instead of starting every account with no calendars.
const ravenCalendarCarryoverTable = `CREATE TABLE IF NOT EXISTS raven_calendar_carryover (
	account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	remote_id TEXT NOT NULL,
	selected INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (account_id, remote_id)
)`

// migrateV106ToV107 drops Raven's calendar cache so upstream's calendar schema
// can be created in its place. Events are only a provider cache (Raven wrote
// changes straight to the provider); the calendar list and selection are kept
// in raven_calendar_carryover.
func migrateV106ToV107(tx *sql.Tx) error {
	if _, err := tx.Exec(ravenCalendarCarryoverTable); err != nil {
		return fmt.Errorf("create calendar carryover: %w", err)
	}
	hasCalendars, err := tableExistsTx(tx, "calendars")
	if err != nil {
		return fmt.Errorf("check Raven calendars: %w", err)
	}
	if hasCalendars {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO raven_calendar_carryover (account_id, remote_id, selected)
			SELECT account_id, provider_calendar_id, MAX(selected) FROM calendars
			WHERE account_id IN (SELECT id FROM accounts)
			GROUP BY account_id, provider_calendar_id`); err != nil {
			return fmt.Errorf("keep Raven calendar selection: %w", err)
		}
	}
	for _, statement := range []string{
		`DROP TABLE IF EXISTS calendar_events`,
		`DROP TABLE IF EXISTS calendars`,
		`DROP TABLE IF EXISTS calendar_account_state`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("drop Raven calendar cache: %w", err)
		}
	}
	return markSchemaVersion(tx, 107)
}

// ravenCalendarCarryoverTx returns the account's calendar selection from
// Raven's old calendar, keyed by provider calendar ID. The first discovery
// that finds calendars applies it to newly found sources, then clears it.
func ravenCalendarCarryoverTx(ctx context.Context, tx *sql.Tx, accountID string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT remote_id, selected FROM raven_calendar_carryover WHERE account_id = ?`, accountID)
	if err != nil {
		return nil, fmt.Errorf("load Raven calendar selection: %w", err)
	}
	defer rows.Close()
	selection := make(map[string]bool)
	for rows.Next() {
		var remoteID string
		var selected int
		if err := rows.Scan(&remoteID, &selected); err != nil {
			return nil, fmt.Errorf("scan Raven calendar selection: %w", err)
		}
		selection[ravenCalendarCarryoverKey(remoteID)] = selected == 1
	}
	return selection, rows.Err()
}

// ravenCalendarCarryoverKey matches calendar IDs across Raven's old CalDAV
// client and upstream's. CalDAV IDs are collection URLs whose host can differ
// between discoveries (iCloud moves accounts between pNN-caldav.icloud.com
// hosts), so they match on scheme, registered domain and path, ignoring case,
// default ports and the trailing slash. Other providers' IDs are opaque and
// compared as-is.
func ravenCalendarCarryoverKey(remoteID string) string {
	remoteID = strings.TrimSpace(remoteID)
	parsed, err := url.Parse(remoteID)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return remoteID
	}
	host := strings.ToLower(parsed.Hostname())
	if domain, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		host = domain
	}
	if port := parsed.Port(); port != "" && !(parsed.Scheme == "https" && port == "443") && !(parsed.Scheme == "http" && port == "80") {
		host += ":" + port
	}
	return strings.ToLower(parsed.Scheme) + "://" + host + strings.TrimSuffix(parsed.EscapedPath(), "/") + "/"
}

func clearRavenCalendarCarryoverTx(ctx context.Context, tx *sql.Tx, accountID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM raven_calendar_carryover WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("clear Raven calendar selection: %w", err)
	}
	return nil
}

// RavenCalendarCarryoverAccount is an account whose calendar selection from
// Raven's old calendar still waits for a calendar discovery.
type RavenCalendarCarryoverAccount struct {
	UserID    string
	AccountID string
	Provider  string
}

// RavenCalendarCarryoverAccounts lists every account that still has a
// calendar selection from Raven's old calendar waiting for discovery.
func (db *DB) RavenCalendarCarryoverAccounts(ctx context.Context) ([]RavenCalendarCarryoverAccount, error) {
	rows, err := db.Read().QueryContext(ctx, `
		SELECT DISTINCT a.user_id, a.id, a.provider
		FROM raven_calendar_carryover c
		JOIN accounts a ON a.id = c.account_id
		WHERE COALESCE(a.is_deleting, 0) = 0
		ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accounts []RavenCalendarCarryoverAccount
	for rows.Next() {
		var account RavenCalendarCarryoverAccount
		if err := rows.Scan(&account.UserID, &account.AccountID, &account.Provider); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}
