package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ErrFolderEmptyUnsupported means the folder is not Spam/Junk or Trash, the
// only folders "Empty folder" may permanently clear.
var ErrFolderEmptyUnsupported = errors.New("only Spam and Trash can be emptied")

// EmptyFolderTarget is one owned Spam/Trash folder and the messages in it.
type EmptyFolderTarget struct {
	AccountID  string
	FolderID   string
	MessageIDs []int64
}

// FolderEmptyTargets returns the owned Spam/Junk or Trash folders addressed by
// folderID (a real folder id, or the unified "spam"/"trash" id) with their live
// messages. It returns sql.ErrNoRows when nothing owned matches.
func (db *DB) FolderEmptyTargets(ctx context.Context, userID, folderID string) ([]EmptyFolderTarget, error) {
	userID = strings.TrimSpace(userID)
	folderID = strings.TrimSpace(folderID)
	where := `a.user_id = ? AND COALESCE(a.is_deleting, 0) = 0 AND COALESCE(f.discovery_state, 'active') = 'active'`
	args := []any{userID}
	if isUnifiedFolderID(folderID) {
		if folderID != "spam" && folderID != "trash" {
			return nil, ErrFolderEmptyUnsupported
		}
		rolePredicate, roleArgs := unifiedFolderRolePredicate("f", folderID)
		accountFilter, accountArgs, err := db.unifiedFolderAccountFilter(ctx, userID, folderID, "a")
		if err != nil {
			return nil, err
		}
		where += " AND " + rolePredicate + accountFilter
		args = append(append(args, roleArgs...), accountArgs...)
	} else {
		where += " AND f.id = ? AND f.role IN ('spam', 'junk', 'trash')"
		args = append(args, folderID)
	}
	rows, err := db.Read().QueryContext(ctx, `SELECT f.id, f.account_id FROM folders f JOIN accounts a ON a.id = f.account_id WHERE `+where+` ORDER BY f.id`, args...)
	if err != nil {
		return nil, err
	}
	var targets []EmptyFolderTarget
	for rows.Next() {
		var t EmptyFolderTarget
		if err := rows.Scan(&t.FolderID, &t.AccountID); err != nil {
			rows.Close()
			return nil, err
		}
		targets = append(targets, t)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		if !isUnifiedFolderID(folderID) {
			var exists int
			err := db.Read().QueryRowContext(ctx, `SELECT 1 FROM folders f JOIN accounts a ON a.id = f.account_id WHERE f.id = ? AND a.user_id = ?`, folderID, userID).Scan(&exists)
			if err == nil {
				return nil, ErrFolderEmptyUnsupported
			}
		}
		return nil, sql.ErrNoRows
	}
	for i := range targets {
		ids, err := db.Read().QueryContext(ctx, `SELECT message_id FROM message_folder_state WHERE folder_id = ? AND is_deleted = 0`, targets[i].FolderID)
		if err != nil {
			return nil, err
		}
		for ids.Next() {
			var id int64
			if err := ids.Scan(&id); err != nil {
				ids.Close()
				return nil, err
			}
			targets[i].MessageIDs = append(targets[i].MessageIDs, id)
		}
		if err := ids.Close(); err != nil {
			return nil, err
		}
	}
	return targets, nil
}
