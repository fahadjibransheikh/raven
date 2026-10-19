package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// migrateV123ToV124 moves users whose saved compose view is the old "dialog"
// default onto the new inline default ("pane"). The client saves its whole
// settings cache, so a stored "dialog" is indistinguishable from "never changed"
// and is treated as default, once, as migrateV98ToV99 did for the list width.
// Rolling back means setting the three keys back to "dialog".
func migrateV123ToV124(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var hasUserID int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('app_settings') WHERE name = 'user_id'`).Scan(&hasUserID); err != nil {
		return fmt.Errorf("inspect app_settings: %w", err)
	}
	if hasUserID > 0 {
		type row struct{ userID, value string }
		var rows []row
		cursor, err := tx.Query(`SELECT user_id, value FROM app_settings WHERE key = 'ui_settings'`)
		if err != nil {
			return fmt.Errorf("read ui settings: %w", err)
		}
		for cursor.Next() {
			var r row
			if err := cursor.Scan(&r.userID, &r.value); err != nil {
				cursor.Close()
				return fmt.Errorf("scan ui settings: %w", err)
			}
			rows = append(rows, r)
		}
		if err := cursor.Close(); err != nil {
			return err
		}
		for _, r := range rows {
			var settings map[string]json.RawMessage
			if json.Unmarshal([]byte(r.value), &settings) != nil {
				continue // unreadable rows already fall back to defaults; leave them
			}
			changed := false
			for _, key := range []string{"default_compose_view", "default_new_compose_view", "default_reply_compose_view"} {
				var view string
				if json.Unmarshal(settings[key], &view) == nil && view == "dialog" {
					settings[key] = json.RawMessage(`"pane"`)
					changed = true
				}
			}
			if !changed {
				continue
			}
			updated, err := json.Marshal(settings)
			if err != nil {
				return fmt.Errorf("encode ui settings: %w", err)
			}
			if _, err := tx.Exec(`UPDATE app_settings SET value = ? WHERE user_id = ? AND key = 'ui_settings'`, string(updated), r.userID); err != nil {
				return fmt.Errorf("update ui settings: %w", err)
			}
		}
	}
	if err := markSchemaVersion(tx, 124); err != nil {
		return err
	}
	return tx.Commit()
}
