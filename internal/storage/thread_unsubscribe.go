package storage

import (
	"context"
	"strings"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

// FillThreadListUnsubscribe sets the stored List-Unsubscribe headers on thread items.
// Items must already be user-scoped (from GetThreadMessagesForUser).
func (db *DB) FillThreadListUnsubscribe(ctx context.Context, items []models.ThreadItem) error {
	if len(items) < 2 {
		return nil
	}
	args := make([]any, len(items))
	index := make(map[string]int, len(items))
	for i, it := range items {
		args[i] = it.ID
		index[it.ID] = i
	}
	rows, err := db.Read().QueryContext(ctx,
		`SELECT id, COALESCE(list_unsubscribe, ''), COALESCE(list_unsubscribe_post, '') FROM messages WHERE id IN (`+
			strings.TrimSuffix(strings.Repeat("?,", len(items)), ",")+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, lu, post string
		if err := rows.Scan(&id, &lu, &post); err != nil {
			return err
		}
		if i, ok := index[id]; ok {
			items[i].ListUnsubscribe, items[i].ListUnsubscribePost = lu, post
		}
	}
	return rows.Err()
}
