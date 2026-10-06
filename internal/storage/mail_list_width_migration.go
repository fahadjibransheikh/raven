package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// migrateV98ToV99 moves users whose saved mail list width is the old 50%
// default onto the current default. The redesign changed the default from 50% to
// 30%, but anyone who ever saved settings has the old default stored verbatim.
// A stored 50% is indistinguishable from "never changed", so it is treated as
// default, once. Any other width is left alone.
func (db *DB) migrateV98ToV99(ctx context.Context) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mail list width migration: %w", err)
	}
	defer tx.Rollback()

	type row struct{ userID, value string }
	var rows []row
	hasUserSettings, err := hasUserScopedSettings(ctx, tx)
	if err != nil {
		return err
	}
	if !hasUserSettings {
		return db.markSchemaV99(ctx, tx)
	}
	cursor, err := tx.QueryContext(ctx, `SELECT user_id, value FROM app_settings WHERE key = 'ui_settings'`)
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

	newDefault := defaultUISettings()["mail_list_width"]
	for _, r := range rows {
		var settings map[string]json.RawMessage
		if json.Unmarshal([]byte(r.value), &settings) != nil {
			continue
		}
		var width string
		if json.Unmarshal(settings["mail_list_width"], &width) != nil || !isOldDefaultMailListWidth(width) {
			continue
		}
		settings["mail_list_width"], _ = json.Marshal(newDefault)
		updated, err := json.Marshal(settings)
		if err != nil {
			return fmt.Errorf("encode ui settings: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE app_settings SET value = ? WHERE user_id = ? AND key = 'ui_settings'`, string(updated), r.userID); err != nil {
			return fmt.Errorf("update ui settings: %w", err)
		}
	}
	return db.markSchemaV99(ctx, tx)
}

func (db *DB) markSchemaV99(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO schema_version (version) VALUES (99)`); err != nil {
		return fmt.Errorf("mark schema version 99: %w", err)
	}
	return tx.Commit()
}

// isOldDefaultMailListWidth matches 50% however it was formatted ("50%",
// "50.0%", " 50 %"); a bare number is pixels on the client and never matches.
func isOldDefaultMailListWidth(v string) bool {
	v = strings.TrimSpace(v)
	num, ok := strings.CutSuffix(v, "%")
	if !ok {
		return false
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
	return err == nil && n == 50
}

// hasUserScopedSettings is false for upgrade-path databases that predate
// app_settings.user_id; there is nothing for a settings migration to rewrite.
func hasUserScopedSettings(ctx context.Context, tx *sql.Tx) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('app_settings') WHERE name = 'user_id'`).Scan(&n); err != nil {
		return false, fmt.Errorf("inspect app_settings: %w", err)
	}
	return n > 0, nil
}
