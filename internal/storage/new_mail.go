package storage

import "context"

// NewInboxMessage is a message stored since a sync pass began that sits in one
// of the account's inbox folders.
type NewInboxMessage struct {
	FolderID       string
	FolderRemoteID string
	Message        SyncMessage
}

// MaxMessageID is the highest message row id, the mark a sync pass takes before
// it stores anything. Row ids only grow, so later inserts sort above it.
func (db *DB) MaxMessageID(ctx context.Context) (int64, error) {
	var id int64
	err := db.Read().QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM messages`).Scan(&id)
	return id, err
}

// NewInboxMessagesAfter lists, newest first, the account's inbox messages whose
// rows were inserted after afterID. Provider syncs only insert a row for mail
// they have not stored before, so the id tells new mail from updates.
func (db *DB) NewInboxMessagesAfter(ctx context.Context, accountID string, afterID int64, limit int) ([]NewInboxMessage, error) {
	rows, err := db.Read().QueryContext(ctx, `
		SELECT f.id, f.remote_id, m.subject, m.from_name, m.from_email,
		       COALESCE(NULLIF(m.preview_text, ''), m.snippet, ''), mfs.is_read
		FROM messages m
		JOIN message_folder_state mfs ON mfs.message_id = m.id AND mfs.is_deleted = 0
		JOIN folders f ON f.id = mfs.folder_id AND f.role = 'inbox'
		WHERE m.account_id = ? AND m.id > ?
		ORDER BY datetime(m.date_received) DESC, m.id DESC
		LIMIT ?`, accountID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NewInboxMessage
	for rows.Next() {
		var n NewInboxMessage
		if err := rows.Scan(&n.FolderID, &n.FolderRemoteID, &n.Message.Subject, &n.Message.FromName, &n.Message.FromEmail, &n.Message.Snippet, &n.Message.IsRead); err != nil {
			return nil, err
		}
		n.Message.AccountID = accountID
		out = append(out, n)
	}
	return out, rows.Err()
}
