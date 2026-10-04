package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

var (
	// ErrIdentityNotFound covers a missing identity, a missing account, and an
	// account owned by someone else: callers must not be able to tell them apart.
	ErrIdentityNotFound  = errors.New("identity not found")
	ErrIdentityInvalid   = errors.New("enter a valid email address")
	ErrIdentityDuplicate = errors.New("that address is already a sending address for this account")
	ErrIdentityProtected = errors.New("the primary address cannot be removed")
)

// MaxIdentitySuggestions caps IdentitySuggestions per account.
const MaxIdentitySuggestions = 5

// identitySuggestionThreshold is the number of received messages an address must
// appear on (To/Cc) before it is suggested.
const identitySuggestionThreshold = 3

// ProviderIdentity is one accepted provider-side send-as address.
type ProviderIdentity struct {
	Email string
	Name  string
}

// NormalizeIdentityEmail parses a bare address and returns it lowercase-trimmed.
// It also returns the display name if the input was "Name <addr>".
func NormalizeIdentityEmail(raw string) (email, name string, err error) {
	addr, perr := mail.ParseAddress(strings.TrimSpace(raw))
	if perr != nil {
		return "", "", ErrIdentityInvalid
	}
	email = strings.ToLower(strings.TrimSpace(addr.Address))
	if email == "" || strings.ContainsAny(email, " \t\r\n,;<>") || strings.Count(email, "@") != 1 {
		return "", "", ErrIdentityInvalid
	}
	return email, strings.TrimSpace(addr.Name), nil
}

const identityColumns = `ai.id, ai.account_id, ai.email, ai.name, ai.source, ai.is_default`

func scanIdentity(scan func(...any) error) (models.AccountIdentity, error) {
	var id models.AccountIdentity
	var isDefault int
	if err := scan(&id.ID, &id.AccountID, &id.Email, &id.Name, &id.Source, &isDefault); err != nil {
		return id, err
	}
	id.IsDefault = isDefault == 1
	return id, nil
}

// ownedAccountPrimaryTx returns the account's primary address, or
// ErrIdentityNotFound if the account is missing, deleting, or not userID's.
func ownedAccountPrimaryTx(ctx context.Context, tx *sql.Tx, userID, accountID string) (string, error) {
	var email string
	err := tx.QueryRowContext(ctx,
		`SELECT lower(trim(email_address)) FROM accounts WHERE id = ? AND user_id = ? AND COALESCE(is_deleting, 0) = 0`,
		accountID, userID).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrIdentityNotFound
	}
	return email, err
}

// ListAccountIdentities returns the identities of one account the user owns
// (primary first). A foreign or missing account yields an empty list.
func (db *DB) ListAccountIdentities(ctx context.Context, userID, accountID string) ([]models.AccountIdentity, error) {
	rows, err := db.Read().QueryContext(ctx, `
		SELECT `+identityColumns+`
		FROM account_identities ai
		JOIN accounts a ON a.id = ai.account_id
		WHERE a.id = ? AND a.user_id = ? AND COALESCE(a.is_deleting, 0) = 0
		ORDER BY CASE ai.source WHEN 'primary' THEN 0 ELSE 1 END, ai.email`, accountID, userID)
	if err != nil {
		return nil, fmt.Errorf("query account identities: %w", err)
	}
	defer rows.Close()
	var out []models.AccountIdentity
	for rows.Next() {
		id, err := scanIdentity(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan account identity: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListUserIdentities returns the identities of all of the user's non-deleting
// accounts, keyed by account ID, in one query.
func (db *DB) ListUserIdentities(ctx context.Context, userID string) (map[string][]models.AccountIdentity, error) {
	rows, err := db.Read().QueryContext(ctx, `
		SELECT `+identityColumns+`
		FROM account_identities ai
		JOIN accounts a ON a.id = ai.account_id
		WHERE a.user_id = ? AND COALESCE(a.is_deleting, 0) = 0
		ORDER BY ai.account_id, CASE ai.source WHEN 'primary' THEN 0 ELSE 1 END, ai.email`, userID)
	if err != nil {
		return nil, fmt.Errorf("query user identities: %w", err)
	}
	defer rows.Close()
	out := make(map[string][]models.AccountIdentity)
	for rows.Next() {
		id, err := scanIdentity(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan account identity: %w", err)
		}
		out[id.AccountID] = append(out[id.AccountID], id)
	}
	return out, rows.Err()
}

// AttachAccountIdentities fills Account.Identities with a single query.
func (db *DB) AttachAccountIdentities(ctx context.Context, userID string, accounts []models.Account) error {
	if len(accounts) == 0 {
		return nil
	}
	byAccount, err := db.ListUserIdentities(ctx, userID)
	if err != nil {
		return err
	}
	for i := range accounts {
		accounts[i].Identities = byAccount[accounts[i].ID]
	}
	return nil
}

// IdentityForAccount returns the identity with the given address on an account
// the user owns, or ErrIdentityNotFound. Compose uses it to validate a chosen From.
func (db *DB) IdentityForAccount(ctx context.Context, userID, accountID, email string) (models.AccountIdentity, error) {
	normalized, _, err := NormalizeIdentityEmail(email)
	if err != nil {
		return models.AccountIdentity{}, ErrIdentityNotFound
	}
	id, err := scanIdentity(db.Read().QueryRowContext(ctx, `
		SELECT `+identityColumns+`
		FROM account_identities ai
		JOIN accounts a ON a.id = ai.account_id
		WHERE a.id = ? AND a.user_id = ? AND COALESCE(a.is_deleting, 0) = 0 AND ai.email = ?`,
		accountID, userID, normalized).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return models.AccountIdentity{}, ErrIdentityNotFound
	}
	return id, err
}

// AddManualIdentity adds a user-entered sending address.
func (db *DB) AddManualIdentity(ctx context.Context, userID, accountID, rawEmail, name string) (models.AccountIdentity, error) {
	email, parsedName, err := NormalizeIdentityEmail(rawEmail)
	if err != nil {
		return models.AccountIdentity{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = parsedName
	}
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return models.AccountIdentity{}, err
	}
	defer tx.Rollback()
	primary, err := ownedAccountPrimaryTx(ctx, tx, userID, accountID)
	if err != nil {
		return models.AccountIdentity{}, err
	}
	if email == primary {
		return models.AccountIdentity{}, ErrIdentityDuplicate
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_identities WHERE account_id = ? AND email = ?`, accountID, email).Scan(&exists); err != nil {
		return models.AccountIdentity{}, err
	}
	if exists > 0 {
		return models.AccountIdentity{}, ErrIdentityDuplicate
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO account_identities (account_id, email, name, source, is_default) VALUES (?, ?, ?, 'manual', 0)`,
		accountID, email, name)
	if err != nil {
		return models.AccountIdentity{}, fmt.Errorf("insert identity: %w", err)
	}
	id, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return models.AccountIdentity{}, err
	}
	return models.AccountIdentity{ID: id, AccountID: accountID, Email: email, Name: name, Source: models.IdentitySourceManual}, nil
}

// DeleteIdentity removes a manual or provider identity. The primary identity is
// protected. Removing the default hands the default back to the primary.
func (db *DB) DeleteIdentity(ctx context.Context, userID, accountID string, identityID int64) error {
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := ownedAccountPrimaryTx(ctx, tx, userID, accountID); err != nil {
		return err
	}
	var source string
	var isDefault int
	err = tx.QueryRowContext(ctx, `SELECT source, is_default FROM account_identities WHERE id = ? AND account_id = ?`, identityID, accountID).Scan(&source, &isDefault)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrIdentityNotFound
	}
	if err != nil {
		return err
	}
	if source == models.IdentitySourcePrimary {
		return ErrIdentityProtected
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_identities WHERE id = ?`, identityID); err != nil {
		return err
	}
	if isDefault == 1 {
		if err := resetDefaultToPrimaryTx(ctx, tx, accountID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// resetDefaultToPrimaryTx makes the primary the default again and clears the
// "user chose a default" flag, since that choice no longer exists.
func resetDefaultToPrimaryTx(ctx context.Context, tx *sql.Tx, accountID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE account_identities SET is_default = 0 WHERE account_id = ? AND is_default = 1`, accountID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_identities SET is_default = 1 WHERE account_id = ? AND source = 'primary'`, accountID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE accounts SET identity_default_pinned = 0 WHERE id = ?`, accountID)
	return err
}

// SetDefaultIdentity makes one identity the account's default and records that
// the user chose it, so provider sync stops overriding it.
func (db *DB) SetDefaultIdentity(ctx context.Context, userID, accountID string, identityID int64) error {
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := ownedAccountPrimaryTx(ctx, tx, userID, accountID); err != nil {
		return err
	}
	var found int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_identities WHERE id = ? AND account_id = ?`, identityID, accountID).Scan(&found); err != nil {
		return err
	}
	if found == 0 {
		return ErrIdentityNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_identities SET is_default = 0, updated_at = CURRENT_TIMESTAMP WHERE account_id = ? AND is_default = 1 AND id != ?`, accountID, identityID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_identities SET is_default = 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, identityID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET identity_default_pinned = 1 WHERE id = ?`, accountID); err != nil {
		return err
	}
	return tx.Commit()
}

// SyncPrimaryIdentityTx makes the account's primary identity mirror
// accounts.email_address / display_name. Call it in the same transaction that
// creates or edits the account. If a manual/provider identity already uses the
// new address it is absorbed (and its default flag inherited).
func SyncPrimaryIdentityTx(ctx context.Context, tx *sql.Tx, accountID string) error {
	var rawEmail, name string
	if err := tx.QueryRowContext(ctx, `SELECT email_address, COALESCE(display_name, '') FROM accounts WHERE id = ?`, accountID).Scan(&rawEmail, &name); err != nil {
		return err
	}
	email := strings.ToLower(strings.TrimSpace(rawEmail))
	if email == "" {
		return nil
	}
	name = strings.TrimSpace(name)

	inheritDefault := false
	var otherID int64
	var otherDefault int
	err := tx.QueryRowContext(ctx, `SELECT id, is_default FROM account_identities WHERE account_id = ? AND email = ? AND source != 'primary'`, accountID, email).Scan(&otherID, &otherDefault)
	if err == nil {
		inheritDefault = otherDefault == 1
		if _, err := tx.ExecContext(ctx, `DELETE FROM account_identities WHERE id = ?`, otherID); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var primaryID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM account_identities WHERE account_id = ? AND source = 'primary'`, accountID).Scan(&primaryID)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO account_identities (account_id, email, name, source, is_default)
			VALUES (?, ?, ?, 'primary', CASE WHEN ? = 1 OR NOT EXISTS (SELECT 1 FROM account_identities WHERE account_id = ? AND is_default = 1) THEN 1 ELSE 0 END)`,
			accountID, email, name, boolToInt(inheritDefault), accountID)
		return err
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE account_identities
		SET email = ?, name = ?, is_default = CASE WHEN ? = 1 THEN 1 ELSE is_default END, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, email, name, boolToInt(inheritDefault), primaryID)
	return err
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// IdentitiesSyncedAt reports when provider aliases were last fetched for the account.
func (db *DB) IdentitiesSyncedAt(ctx context.Context, accountID string) (time.Time, bool, error) {
	var at sql.NullTime
	err := db.Read().QueryRowContext(ctx, `SELECT identities_synced_at FROM accounts WHERE id = ?`, accountID).Scan(&at)
	if err != nil {
		return time.Time{}, false, err
	}
	return at.Time, at.Valid, nil
}

// TouchIdentitiesSyncedAt records a provider fetch attempt (successful or not)
// so the sync path can throttle itself.
func (db *DB) TouchIdentitiesSyncedAt(ctx context.Context, accountID string, at time.Time) error {
	_, err := db.Write().ExecContext(ctx, `UPDATE accounts SET identities_synced_at = ? WHERE id = ?`, at.UTC(), accountID)
	return err
}

// ApplyProviderIdentities reconciles provider-sourced identities with the
// accepted aliases the provider returned. Trusted sync-path method: accountID
// comes from an enumerated sync, not from a browser request. Manual identities
// are never touched. providerDefault (may be empty) becomes the default unless
// the user has explicitly picked one.
func (db *DB) ApplyProviderIdentities(ctx context.Context, accountID string, aliases []ProviderIdentity, providerDefault string, now time.Time) error {
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var primary string
	var pinned int
	if err := tx.QueryRowContext(ctx, `SELECT lower(trim(email_address)), identity_default_pinned FROM accounts WHERE id = ? AND COALESCE(is_deleting, 0) = 0`, accountID).Scan(&primary, &pinned); err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, alias := range aliases {
		email, _, err := NormalizeIdentityEmail(alias.Email)
		if err != nil || email == primary {
			continue
		}
		keep[email] = true
		// The WHERE keeps a same-address manual identity untouched.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO account_identities (account_id, email, name, source, is_default) VALUES (?, ?, ?, 'provider', 0)
			ON CONFLICT(account_id, email) DO UPDATE SET name = excluded.name, updated_at = CURRENT_TIMESTAMP
			WHERE account_identities.source = 'provider'`, accountID, email, strings.TrimSpace(alias.Name)); err != nil {
			return err
		}
	}

	rows, err := tx.QueryContext(ctx, `SELECT id, email, is_default FROM account_identities WHERE account_id = ? AND source = 'provider'`, accountID)
	if err != nil {
		return err
	}
	type stale struct {
		id        int64
		isDefault bool
	}
	var gone []stale
	for rows.Next() {
		var id int64
		var email string
		var isDefault int
		if err := rows.Scan(&id, &email, &isDefault); err != nil {
			rows.Close()
			return err
		}
		if !keep[email] {
			gone = append(gone, stale{id, isDefault == 1})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, s := range gone {
		if _, err := tx.ExecContext(ctx, `DELETE FROM account_identities WHERE id = ?`, s.id); err != nil {
			return err
		}
		if s.isDefault {
			if err := resetDefaultToPrimaryTx(ctx, tx, accountID); err != nil {
				return err
			}
			pinned = 0
		}
	}

	if def, _, err := NormalizeIdentityEmail(providerDefault); err == nil && pinned == 0 {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM account_identities WHERE account_id = ? AND email = ?`, accountID, def).Scan(&id)
		if err == nil {
			if _, err := tx.ExecContext(ctx, `UPDATE account_identities SET is_default = 0 WHERE account_id = ? AND is_default = 1 AND id != ?`, accountID, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE account_identities SET is_default = 1 WHERE id = ?`, id); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET identities_synced_at = ? WHERE id = ?`, now.UTC(), accountID); err != nil {
		return err
	}
	return tx.Commit()
}

// IdentitySuggestions returns up to MaxIdentitySuggestions addresses that appear
// as To/Cc recipients on at least 3 received messages of the account and are
// neither an identity nor dismissed.
func (db *DB) IdentitySuggestions(ctx context.Context, userID, accountID string) ([]models.IdentitySuggestion, error) {
	out, err := db.identitySuggestions(ctx, userID, accountID)
	return out[accountID], err
}

// IdentitySuggestionsForUser returns suggestions for every account (settings page)
// with one query for all of the user's accounts.
func (db *DB) IdentitySuggestionsForUser(ctx context.Context, userID string) (map[string][]models.IdentitySuggestion, error) {
	return db.identitySuggestions(ctx, userID, "")
}

func (db *DB) identitySuggestions(ctx context.Context, userID, accountID string) (map[string][]models.IdentitySuggestion, error) {
	args := []any{userID}
	accountFilter := ""
	if accountID != "" {
		accountFilter = " AND a.id = ?"
		args = append(args, accountID)
	}
	// "Received" = not a draft, not in a Sent/Drafts folder, and not sent from one
	// of our own addresses. List-Id / Delivered-To headers are not stored, so a
	// few list-looking addresses are skipped by pattern.
	// ponytail: pattern skip is crude; store List-Id at sync time if it proves noisy.
	rows, err := db.Read().QueryContext(ctx, `
		SELECT account_id, email, n FROM (
			SELECT c.account_id, c.email, c.n,
			       ROW_NUMBER() OVER (PARTITION BY c.account_id ORDER BY c.n DESC, c.email) AS rn
			FROM (
				SELECT m.account_id AS account_id, lower(trim(mr.email)) AS email, COUNT(DISTINCT m.id) AS n
				FROM message_recipients mr
				JOIN messages m ON m.id = mr.message_id
				JOIN accounts a ON a.id = m.account_id
				WHERE a.user_id = ? AND COALESCE(a.is_deleting, 0) = 0`+accountFilter+`
				  AND mr.kind IN ('to', 'cc')
				  AND trim(mr.email) != ''
				  AND lower(trim(m.from_email)) != lower(trim(a.email_address))
				  AND lower(trim(m.from_email)) NOT IN (SELECT ai.email FROM account_identities ai WHERE ai.account_id = m.account_id)
				  AND NOT EXISTS (
				      SELECT 1 FROM message_folder_state s JOIN folders f ON f.id = s.folder_id
				      WHERE s.message_id = m.id AND (s.is_draft = 1 OR f.role IN ('sent', 'drafts')))
				GROUP BY m.account_id, lower(trim(mr.email))
				HAVING COUNT(DISTINCT m.id) >= `+fmt.Sprint(identitySuggestionThreshold)+`
			) c
			JOIN accounts a2 ON a2.id = c.account_id
			WHERE c.email != lower(trim(a2.email_address))
			  AND c.email NOT LIKE '%@lists.%' AND c.email NOT LIKE '%-list@%' AND c.email NOT LIKE '%-announce@%'
			  AND NOT EXISTS (SELECT 1 FROM account_identities ai WHERE ai.account_id = c.account_id AND ai.email = c.email)
			  AND NOT EXISTS (SELECT 1 FROM account_identity_dismissals d WHERE d.account_id = c.account_id AND d.email = c.email)
		) WHERE rn <= `+fmt.Sprint(MaxIdentitySuggestions)+`
		ORDER BY account_id, n DESC, email`, args...)
	if err != nil {
		return nil, fmt.Errorf("query identity suggestions: %w", err)
	}
	defer rows.Close()
	out := make(map[string][]models.IdentitySuggestion)
	for rows.Next() {
		var acc string
		var s models.IdentitySuggestion
		if err := rows.Scan(&acc, &s.Email, &s.Count); err != nil {
			return nil, err
		}
		out[acc] = append(out[acc], s)
	}
	return out, rows.Err()
}

// DismissIdentitySuggestion hides an address from future suggestions.
func (db *DB) DismissIdentitySuggestion(ctx context.Context, userID, accountID, rawEmail string) error {
	email, _, err := NormalizeIdentityEmail(rawEmail)
	if err != nil {
		return err
	}
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := ownedAccountPrimaryTx(ctx, tx, userID, accountID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO account_identity_dismissals (account_id, email) VALUES (?, ?)`, accountID, email); err != nil {
		return err
	}
	return tx.Commit()
}

// accountSelfAddressesTx returns every address that means "me" for an account:
// the account address plus all identities.
func accountSelfAddressesTx(ctx context.Context, tx *sql.Tx, accountID string) map[string]bool {
	out := map[string]bool{}
	var primary string
	if tx.QueryRowContext(ctx, `SELECT lower(trim(email_address)) FROM accounts WHERE id = ?`, accountID).Scan(&primary) == nil && primary != "" {
		out[primary] = true
	}
	rows, err := tx.QueryContext(ctx, `SELECT email FROM account_identities WHERE account_id = ?`, accountID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var email string
		if rows.Scan(&email) == nil {
			out[email] = true
		}
	}
	return out
}
