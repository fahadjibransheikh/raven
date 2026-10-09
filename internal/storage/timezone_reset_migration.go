package storage

import (
	"context"
	"encoding/json"
	"fmt"
)

// migrateV102ToV103 resets every stored ui timezone to "local". The web client
// used to overwrite "local" with the browser's zone at that moment and persist
// it, so stored zones are almost all stale auto-writes (a traveller kept seeing
// the old zone). "local" now resolves per request from the raven_tz cookie.
func (db *DB) migrateV102ToV103(ctx context.Context) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin timezone reset migration: %w", err)
	}
	defer tx.Rollback()

	hasUserSettings, err := hasUserScopedSettings(ctx, tx)
	if err != nil {
		return err
	}
	if hasUserSettings {
		rows, err := tx.QueryContext(ctx, `SELECT user_id, value FROM app_settings WHERE key = 'ui_settings'`)
		if err != nil {
			return fmt.Errorf("read ui settings: %w", err)
		}
		updates := map[string]string{}
		for rows.Next() {
			var userID, value string
			if err := rows.Scan(&userID, &value); err != nil {
				rows.Close()
				return fmt.Errorf("scan ui settings: %w", err)
			}
			var settings map[string]json.RawMessage
			if json.Unmarshal([]byte(value), &settings) != nil {
				continue
			}
			var tz string
			if json.Unmarshal(settings["timezone"], &tz) != nil || tz == "" || tz == "local" {
				continue
			}
			settings["timezone"], _ = json.Marshal("local")
			updated, err := json.Marshal(settings)
			if err != nil {
				rows.Close()
				return fmt.Errorf("encode ui settings: %w", err)
			}
			updates[userID] = string(updated)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for userID, value := range updates {
			if _, err := tx.ExecContext(ctx, `UPDATE app_settings SET value = ? WHERE user_id = ? AND key = 'ui_settings'`, value, userID); err != nil {
				return fmt.Errorf("update ui settings: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO schema_version (version) VALUES (103)`); err != nil {
		return fmt.Errorf("mark schema version 103: %w", err)
	}
	return tx.Commit()
}
