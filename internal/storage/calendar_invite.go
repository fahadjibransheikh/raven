package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

// FindCalendarEventByICalUID finds the synced event an invitation email refers
// to, among every calendar (selected or not) of one of the user's accounts.
// A recurring invite shares its iCalUID across instances; the series master
// (no recurring_event_id) wins, else the earliest instance. A foreign account
// or user yields ErrCalendarNotFound, same as no match.
func (db *DB) FindCalendarEventByICalUID(ctx context.Context, userID, accountID, uid string) (models.CalendarEventView, error) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return models.CalendarEventView{}, ErrCalendarNotFound
	}
	v, err := scanCalendarEventView(db.Read().QueryRowContext(ctx, `
		SELECT `+calendarEventViewColumns+`
		FROM calendar_events e
		JOIN calendars c ON c.id = e.calendar_id
		JOIN accounts a ON a.id = c.account_id
		WHERE a.user_id = ? AND a.id = ? AND COALESCE(a.is_deleting, 0) = 0 AND e.ical_uid = ?
		ORDER BY (e.recurring_event_id = '') DESC, e.start_at, e.id
		LIMIT 1`, userID, accountID, uid))
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrCalendarNotFound
	}
	return v, err
}
