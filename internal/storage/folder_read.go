package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrFolderReadUnsupported means the folder has no provider-side identity the
// bulk "mark all as read" operation can target (virtual folders, Gmail's
// folders without a remote name). Gmail's pseudo-archive is supported: it has
// no label, so its job lists with a search query instead.
var ErrFolderReadUnsupported = errors.New("mark all as read is not available for this folder")

// FolderReadMutation is a queued whole-folder read operation. The cutoff makes
// it idempotent and keeps mail that arrives afterwards unread.
type FolderReadMutation struct {
	ID              string
	AccountID       string
	FolderID        string
	ProviderType    string
	CutoffAt        time.Time
	CutoffMessageID int64
	MaxUID          uint32
	UIDValidity     uint32
	Status          string
	AttemptCount    int
}

type FolderReadResult struct {
	AccountID string
	FolderID  string
	Marked    int
	// Skipped marks an account folder in a unified fan-out that has no
	// provider identity to target; nothing was changed for it.
	Skipped     bool
	AccountName string
}

type folderReadTarget struct {
	id, accountID, provider, remoteID, providerRemoteID, accountName string
	uidValidity                                                      int64
}

// folderReadTargets returns the folders a job can target plus, for a unified
// folder only, the ones skipped for lacking a provider identity.
func (db *DB) folderReadTargets(ctx context.Context, userID, folderID string) (out, skipped []folderReadTarget, err error) {
	where := `a.user_id = ? AND COALESCE(a.is_deleting, 0) = 0 AND COALESCE(f.discovery_state, 'active') = 'active'`
	args := []any{userID}
	unified := isUnifiedFolderID(folderID)
	if unified {
		if folderID == "starred" || folderID == "scheduled" {
			return nil, nil, ErrFolderReadUnsupported
		}
		rolePredicate, roleArgs := unifiedFolderRolePredicate("f", folderID)
		accountFilter, accountArgs, err := db.unifiedFolderAccountFilter(ctx, userID, folderID, "a")
		if err != nil {
			return nil, nil, err
		}
		where += " AND " + rolePredicate + accountFilter
		args = append(append(args, roleArgs...), accountArgs...)
	} else {
		where += " AND f.id = ?"
		args = append(args, folderID)
	}
	rows, err := db.Read().QueryContext(ctx, `
		SELECT f.id, f.account_id, a.provider, COALESCE(f.remote_id, ''), COALESCE(f.provider_remote_id, ''), COALESCE(f.uid_validity, 0), COALESCE(NULLIF(a.display_name, ''), a.email_address, a.id)
		FROM folders f JOIN accounts a ON a.id = f.account_id
		WHERE `+where+` ORDER BY f.id`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t folderReadTarget
		if err := rows.Scan(&t.id, &t.accountID, &t.provider, &t.remoteID, &t.providerRemoteID, &t.uidValidity, &t.accountName); err != nil {
			return nil, nil, err
		}
		t.provider = messageMutationProviderType(t.provider)
		supported := false
		switch t.provider {
		case MessageMutationProviderIMAP:
			supported = strings.TrimSpace(t.remoteID) != ""
		case MessageMutationProviderGmail:
			supported = strings.TrimSpace(t.providerRemoteID) != ""
		default:
			supported = strings.TrimSpace(t.providerRemoteID) != ""
		}
		if supported {
			out = append(out, t)
		} else if unified {
			skipped = append(skipped, t)
		} else {
			return nil, nil, ErrFolderReadUnsupported
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if !unified && len(out) == 0 {
		return nil, nil, sql.ErrNoRows
	}
	return out, skipped, nil
}

// MarkFolderReadAndQueueForUser marks every locally known unread message of
// the folder (or, for a unified folder, of each owned account's folder with
// that role) read in one transaction and queues one provider-side job per
// folder. No per-message read mutations are created; pending per-message read
// mutations for the affected messages are removed so they cannot flip the
// message back on the server.
//
// The local cutoff is the highest messages.id at transaction time: ids are
// AUTOINCREMENT, so anything synced afterwards has a larger id and stays
// unread regardless of its Date header. cutoff (request time) bounds the
// provider-side query, which filters on provider receive time.
func (db *DB) MarkFolderReadAndQueueForUser(ctx context.Context, userID, folderID string, cutoff time.Time) ([]FolderReadResult, error) {
	userID = strings.TrimSpace(userID)
	folderID = strings.TrimSpace(folderID)
	if userID == "" {
		return nil, fmt.Errorf("folder owner is required")
	}
	targets, skipped, err := db.folderReadTargets(ctx, userID, folderID)
	if err != nil {
		return nil, err
	}
	var results []FolderReadResult
	for _, t := range skipped {
		results = append(results, FolderReadResult{AccountID: t.accountID, FolderID: t.id, Skipped: true, AccountName: t.accountName})
	}
	if len(targets) == 0 {
		return results, nil
	}
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var maxMessageID int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM messages`).Scan(&maxMessageID); err != nil {
		return nil, err
	}
	const unreadInFolder = `SELECT message_id FROM message_folder_state
		WHERE folder_id = ? AND is_deleted = 0 AND is_read = 0 AND message_id <= ?`
	touched := map[string]struct{}{}
	for _, t := range targets {
		var marked int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+unreadInFolder+`)`, t.id, maxMessageID).Scan(&marked); err != nil {
			return nil, err
		}
		imap := t.provider == MessageMutationProviderIMAP
		scope := ""
		if imap {
			scope = t.id
		}
		// IMAP flags are per folder copy; Gmail/Outlook read state is per message.
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT folder_id FROM message_folder_state
			WHERE message_id IN (`+unreadInFolder+`)`, t.id, maxMessageID)
		if err != nil {
			return nil, err
		}
		touched[t.id] = struct{}{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			if !imap {
				touched[id] = struct{}{}
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		// Supersede per-message read mutations (any status, including applied
		// ledger rows that would otherwise override sync with a stale target).
		if _, err := tx.ExecContext(ctx, `DELETE FROM message_mutations
			WHERE kind = ? AND folder_id = ? AND message_id IN (`+unreadInFolder+`)`,
			MessageMutationRead, scope, t.id, maxMessageID); err != nil {
			return nil, fmt.Errorf("supersede read mutations: %w", err)
		}
		if imap {
			_, err = tx.ExecContext(ctx, `UPDATE message_folder_state SET is_read = 1
				WHERE folder_id = ? AND is_deleted = 0 AND is_read = 0 AND message_id <= ?`, t.id, maxMessageID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE message_folder_state SET is_read = 1
				WHERE message_id IN (`+unreadInFolder+`)`, t.id, maxMessageID)
		}
		if err != nil {
			return nil, fmt.Errorf("mark folder read locally: %w", err)
		}
		var maxUID int64
		if imap {
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(remote_uid), 0) FROM message_folder_state WHERE folder_id = ?`, t.id).Scan(&maxUID); err != nil {
				return nil, err
			}
		}
		if imap && maxUID == 0 {
			// Nothing is known on the server side yet; nothing to range over.
			results = append(results, FolderReadResult{AccountID: t.accountID, FolderID: t.id, Marked: marked})
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO folder_read_mutations (
				id, account_id, folder_id, provider_type, cutoff_at, cutoff_message_id, max_uid, uid_validity,
				status, attempt_count, last_error, locked_at, next_attempt_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, '', NULL, CURRENT_TIMESTAMP)
			ON CONFLICT(folder_id) DO UPDATE SET
				cutoff_at = excluded.cutoff_at,
				cutoff_message_id = excluded.cutoff_message_id,
				max_uid = excluded.max_uid,
				uid_validity = excluded.uid_validity,
				status = excluded.status,
				attempt_count = 0,
				last_error = '',
				locked_at = NULL,
				next_attempt_at = CURRENT_TIMESTAMP,
				updated_at = CURRENT_TIMESTAMP`,
			uuid.NewString(), t.accountID, t.id, t.provider, cutoff.UTC(), maxMessageID, maxUID, t.uidValidity, MessageMutationPending); err != nil {
			return nil, fmt.Errorf("queue folder read: %w", err)
		}
		results = append(results, FolderReadResult{AccountID: t.accountID, FolderID: t.id, Marked: marked})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for id := range touched {
		_, _ = db.RefreshFolderUnreadCount(ctx, id)
		_ = db.RefreshFolderThreadState(ctx, id)
	}
	return results, nil
}

// ClaimDueFolderReadMutations is an internal worker boundary, like
// ClaimDueMessageMutations.
func (db *DB) ClaimDueFolderReadMutations(ctx context.Context, now time.Time, limit int) ([]FolderReadMutation, error) {
	if limit <= 0 {
		limit = 5
	}
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, account_id, folder_id, provider_type, cutoff_at, cutoff_message_id, max_uid, uid_validity, status, attempt_count
		FROM folder_read_mutations
		WHERE status IN (?, ?) AND next_attempt_at <= ?
		  AND EXISTS (
			SELECT 1 FROM accounts a JOIN users u ON u.id = a.user_id
			WHERE a.id = folder_read_mutations.account_id AND COALESCE(a.is_deleting, 0) = 0 AND u.status = 'active'
		  )
		ORDER BY created_at ASC LIMIT ?`, MessageMutationPending, MessageMutationFailed, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	var out []FolderReadMutation
	for rows.Next() {
		var m FolderReadMutation
		var cutoff time.Time
		var maxUID, validity int64
		if err := rows.Scan(&m.ID, &m.AccountID, &m.FolderID, &m.ProviderType, &cutoff, &m.CutoffMessageID, &maxUID, &validity, &m.Status, &m.AttemptCount); err != nil {
			rows.Close()
			return nil, err
		}
		m.CutoffAt = cutoff.UTC()
		m.MaxUID, m.UIDValidity = uint32(maxUID), uint32(validity)
		out = append(out, m)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		res, err := tx.ExecContext(ctx, `
			UPDATE folder_read_mutations
			SET status = ?, attempt_count = attempt_count + 1, locked_at = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND status = ?`, MessageMutationProcessing, now.UTC(), out[i].ID, out[i].Status)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return nil, fmt.Errorf("folder read mutation %s was claimed concurrently", out[i].ID)
		}
		out[i].AttemptCount++
	}
	return out, tx.Commit()
}

// CompleteFolderReadMutation removes the job. A job re-armed while it ran is
// pending again, so the status guard leaves it queued.
func (db *DB) CompleteFolderReadMutation(ctx context.Context, id string) error {
	_, err := db.Write().ExecContext(ctx, `DELETE FROM folder_read_mutations WHERE id = ? AND status = ?`, id, MessageMutationProcessing)
	return err
}

func (db *DB) FinishFolderReadMutationWithError(ctx context.Context, id, errorText string, nextAttempt time.Time) error {
	_, err := db.Write().ExecContext(ctx, `
		UPDATE folder_read_mutations
		SET status = ?, last_error = ?, locked_at = NULL, next_attempt_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status = ?`, MessageMutationFailed, errorText, nextAttempt.UTC(), id, MessageMutationProcessing)
	return err
}

// GetFolderReadRemoteInfo returns the provider identities needed to run a job;
// sql.ErrNoRows when the folder is gone.
func (db *DB) GetFolderReadRemoteInfo(ctx context.Context, folderID string) (remoteID, providerRemoteID, role string, err error) {
	err = db.Read().QueryRowContext(ctx, `
		SELECT COALESCE(remote_id, ''), COALESCE(provider_remote_id, ''), COALESCE(role, '')
		FROM folders WHERE id = ?`, folderID).Scan(&remoteID, &providerRemoteID, &role)
	return strings.TrimSpace(remoteID), strings.TrimSpace(providerRemoteID), strings.TrimSpace(role), err
}
