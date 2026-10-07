package storage

import (
	"context"
	"encoding/json"
	"fmt"
)

// oldDefaultMailCardLayout is the card layout that shipped as the default before
// the thread count moved from under the avatar into the meta column.
const oldDefaultMailCardLayout = "railTop:avatar|header:from,date|meta:attachment,unread|railMiddle:|body:subject|status:|railBottom:thread|footer:preview,labels|corner:starred|hidden:account,accountMarker,to"

// migrateV100ToV101 moves users whose stored mail_card_layout is exactly the old
// default onto the current default. A stored old default is indistinguishable
// from "never customised", so it is treated as default, once. Any other layout
// is left alone, and so are rows with no stored layout (they inherit the new
// default) and unreadable rows.
func (db *DB) migrateV100ToV101(ctx context.Context) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mail card layout migration: %w", err)
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
		newDefault := defaultUISettings()["mail_card_layout"]
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
			var layout string
			if json.Unmarshal(settings["mail_card_layout"], &layout) != nil || layout != oldDefaultMailCardLayout {
				continue
			}
			settings["mail_card_layout"], _ = json.Marshal(newDefault)
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
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO schema_version (version) VALUES (101)`); err != nil {
		return fmt.Errorf("mark schema version 101: %w", err)
	}
	return tx.Commit()
}
