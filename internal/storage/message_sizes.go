package storage

import (
	"context"
	"fmt"
	"strings"
)

// Message sizes are 0 while unknown. A size the provider cannot report is
// stored as -1, so the background lookup does not ask for it again.
const MessageSizeUnavailable int64 = -1

// ListFolderUIDsMissingSize returns UIDs in an IMAP folder whose messages have
// no size yet, newest first.
func (db *DB) ListFolderUIDsMissingSize(ctx context.Context, folderID string, limit int) ([]uint32, error) {
	rows, err := db.Read().QueryContext(ctx, `
		SELECT s.remote_uid FROM message_folder_state s
		JOIN messages m ON m.id = s.message_id
		WHERE s.folder_id = ? AND s.remote_uid IS NOT NULL AND s.remote_uid > 0 AND s.is_deleted = 0 AND m.size_bytes = 0
		ORDER BY s.remote_uid DESC LIMIT ?`, folderID, limit)
	if err != nil {
		return nil, fmt.Errorf("list folder UIDs missing size: %w", err)
	}
	defer rows.Close()
	var uids []uint32
	for rows.Next() {
		var uid uint32
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		uids = append(uids, uid)
	}
	return uids, rows.Err()
}

// SetFolderMessageSizes stores sizes reported for UIDs of an IMAP folder.
func (db *DB) SetFolderMessageSizes(ctx context.Context, folderID string, sizes map[uint32]int64) error {
	if len(sizes) == 0 {
		return nil
	}
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
		UPDATE messages SET size_bytes = ?
		WHERE id IN (SELECT message_id FROM message_folder_state WHERE folder_id = ? AND remote_uid = ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for uid, size := range sizes {
		if size == 0 {
			continue
		}
		if _, err := stmt.ExecContext(ctx, size, folderID, uid); err != nil {
			return fmt.Errorf("set message size: %w", err)
		}
	}
	return tx.Commit()
}

// ListProviderMessagesMissingSize returns provider IDs of an account's
// messages that have no size yet, newest first.
func (db *DB) ListProviderMessagesMissingSize(ctx context.Context, accountID string, limit int) ([]string, error) {
	rows, err := db.Read().QueryContext(ctx, `
		SELECT remote_message_id FROM messages
		WHERE account_id = ? AND size_bytes = 0 AND COALESCE(remote_message_id, '') != ''
		ORDER BY date_received DESC, id DESC LIMIT ?`, accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("list messages missing size: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SetProviderMessageSizes stores sizes reported for provider message IDs.
func (db *DB) SetProviderMessageSizes(ctx context.Context, accountID string, sizes map[string]int64) error {
	if len(sizes) == 0 {
		return nil
	}
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `UPDATE messages SET size_bytes = ? WHERE account_id = ? AND remote_message_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for id, size := range sizes {
		id = strings.TrimSpace(id)
		if id == "" || size == 0 {
			continue
		}
		if _, err := stmt.ExecContext(ctx, size, accountID, id); err != nil {
			return fmt.Errorf("set message size: %w", err)
		}
	}
	return tx.Commit()
}
