package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// migrateV123ToV124 moves users whose replies open in the old "dialog" default
// onto the new default, "thread" (the reply opens below the conversation). The
// client saves its whole settings cache, so a stored "dialog" is
// indistinguishable from "never changed" and is treated as default, once, as
// migrateV98ToV99 did for the list width. The reply view resolves like
// GetUISettings: its own key, else the legacy default_compose_view. New mail and
// the legacy key are left alone. Rolling back means setting
// default_reply_compose_view back to "dialog".
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
			var reply, legacy string
			_ = json.Unmarshal(settings["default_reply_compose_view"], &reply)
			_ = json.Unmarshal(settings["default_compose_view"], &legacy)
			if reply == "" {
				reply = legacy
			}
			if reply != "" && reply != "dialog" {
				continue
			}
			settings["default_reply_compose_view"] = json.RawMessage(`"thread"`)
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
