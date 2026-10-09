package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// hideAvatarInMailCardLayout moves the avatar into the hidden zone of a stored
// layout string. It is always listed there explicitly: a field missing from a
// layout is re-added to its default zone, which for the avatar is railTop.
func hideAvatarInMailCardLayout(layout string) string {
	groups := strings.Split(layout, "|")
	hiddenAt := -1
	for i, group := range groups {
		zone, ids, ok := strings.Cut(group, ":")
		if !ok {
			continue
		}
		kept := []string{}
		for _, id := range strings.Split(ids, ",") {
			if id = strings.TrimSpace(id); id != "" && id != "avatar" {
				kept = append(kept, id)
			}
		}
		if strings.TrimSpace(zone) == "hidden" {
			hiddenAt = i
		}
		groups[i] = zone + ":" + strings.Join(kept, ",")
	}
	if hiddenAt == -1 {
		return strings.Join(groups, "|") + "|hidden:avatar"
	}
	zone, ids, _ := strings.Cut(groups[hiddenAt], ":")
	if ids == "" {
		groups[hiddenAt] = zone + ":avatar"
	} else {
		groups[hiddenAt] = zone + ":avatar," + ids
	}
	return strings.Join(groups, "|")
}

// migrateV103ToV104 makes mail rows text-only: every stored mail_card_layout
// loses its avatar to the hidden zone (users can drag it back in the Card layout
// dialog), and avatar is dropped from a stored mail_card_fields, since a
// visible field would put a hidden avatar back in its default zone. A layout
// that is exactly the v102 default becomes the new default, which also shows
// the account colour bar. Runs once; other fields and settings are untouched.
func (db *DB) migrateV103ToV104(ctx context.Context) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin avatar hide migration: %w", err)
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
			changed := false
			var layout string
			isOldDefault := false
			if json.Unmarshal(settings["mail_card_layout"], &layout) == nil && layout != "" {
				isOldDefault = layout == v102DefaultMailCardLayout
				next := hideAvatarInMailCardLayout(layout)
				if isOldDefault {
					next = defaultMailCardLayout
				}
				if next != layout {
					settings["mail_card_layout"], _ = json.Marshal(next)
					changed = true
				}
			}
			var fields string
			if json.Unmarshal(settings["mail_card_fields"], &fields) == nil && fields != "" {
				kept := []string{}
				hasMarker := false
				for _, id := range strings.Split(fields, ",") {
					if id = strings.TrimSpace(id); id != "" && id != "avatar" {
						kept = append(kept, id)
						hasMarker = hasMarker || id == "accountMarker"
					}
				}
				if isOldDefault && !hasMarker {
					kept = append(kept, "accountMarker")
				}
				if next := strings.Join(kept, ","); next != fields {
					settings["mail_card_fields"], _ = json.Marshal(next)
					changed = true
				}
			}
			if !changed {
				continue
			}
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
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO schema_version (version) VALUES (104)`); err != nil {
		return fmt.Errorf("mark schema version 104: %w", err)
	}
	return tx.Commit()
}
