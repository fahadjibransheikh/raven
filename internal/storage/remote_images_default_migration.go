package storage

import (
	"context"
	"encoding/json"
	"fmt"
)

// migrateV99ToV100 pins load_remote_images="true" for every existing user who
// has no stored choice. The default is now "false" (remote content blocked), and
// without this an existing user's effective setting would flip on upgrade. Users
// created after this migration have no row and get the new default. Stored
// choices, including an explicit "false", are never modified.
func (db *DB) migrateV99ToV100(ctx context.Context) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remote images default migration: %w", err)
	}
	defer tx.Rollback()

	hasUserSettings, err := hasUserScopedSettings(ctx, tx)
	if err != nil {
		return err
	}
	if hasUserSettings {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO app_settings (user_id, key, value)
			SELECT id, 'ui_settings', '{"load_remote_images":"true"}' FROM users
			WHERE id NOT IN (SELECT user_id FROM app_settings WHERE key = 'ui_settings')`); err != nil {
			return fmt.Errorf("pin remote images for users without settings: %w", err)
		}
		rows, err := tx.QueryContext(ctx, `SELECT user_id, value FROM app_settings WHERE key = 'ui_settings'`)
		if err != nil {
			return fmt.Errorf("read ui settings: %w", err)
		}
		pinned := map[string]string{}
		for rows.Next() {
			var userID, value string
			if err := rows.Scan(&userID, &value); err != nil {
				rows.Close()
				return fmt.Errorf("scan ui settings: %w", err)
			}
			var settings map[string]json.RawMessage
			if json.Unmarshal([]byte(value), &settings) != nil {
				continue // unreadable rows already fall back to defaults; leave them
			}
			var current string
			if json.Unmarshal(settings["load_remote_images"], &current) == nil && current != "" {
				continue
			}
			settings["load_remote_images"] = json.RawMessage(`"true"`)
			updated, err := json.Marshal(settings)
			if err != nil {
				rows.Close()
				return fmt.Errorf("encode ui settings: %w", err)
			}
			pinned[userID] = string(updated)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for userID, value := range pinned {
			if _, err := tx.ExecContext(ctx, `UPDATE app_settings SET value = ? WHERE user_id = ? AND key = 'ui_settings'`, value, userID); err != nil {
				return fmt.Errorf("update ui settings: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO schema_version (version) VALUES (100)`); err != nil {
		return fmt.Errorf("mark schema version 100: %w", err)
	}
	return tx.Commit()
}
