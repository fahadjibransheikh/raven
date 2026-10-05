package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

// ErrCalendarNotFound covers a missing calendar and one owned by someone else.
var ErrCalendarNotFound = errors.New("calendar not found")

const calendarDateLayout = "2006-01-02"

func nullableDBTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return formatDBTime(*t)
}

func nullTimePtr(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.Time.UTC()
	return &t
}

// UpsertCalendars stores the provider's calendar list for an account. A user's
// "selected" choice survives re-syncs; new calendars default to selected when
// owned or primary. Calendars no longer listed are removed (events cascade).
func (db *DB) UpsertCalendars(ctx context.Context, accountID string, calendars []models.Calendar) error {
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	keep := make([]any, 0, len(calendars)+1)
	keep = append(keep, accountID)
	for _, c := range calendars {
		defaultSelected := 0
		if c.IsPrimary || c.AccessRole == "owner" {
			defaultSelected = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO calendars (account_id, provider_calendar_id, name, color, time_zone, is_primary, access_role, selected)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(account_id, provider_calendar_id) DO UPDATE SET
				name = excluded.name, color = excluded.color, time_zone = excluded.time_zone,
				is_primary = excluded.is_primary, access_role = excluded.access_role`,
			accountID, c.ProviderCalendarID, c.Name, c.Color, c.TimeZone, boolInt(c.IsPrimary), c.AccessRole, defaultSelected); err != nil {
			return fmt.Errorf("upsert calendar: %w", err)
		}
		keep = append(keep, c.ProviderCalendarID)
	}
	del := `DELETE FROM calendars WHERE account_id = ?`
	if len(calendars) > 0 {
		del += ` AND provider_calendar_id NOT IN (?` + strings.Repeat(",?", len(calendars)-1) + `)`
	}
	if _, err := tx.ExecContext(ctx, del, keep...); err != nil {
		return fmt.Errorf("remove stale calendars: %w", err)
	}
	return tx.Commit()
}

const calendarColumns = `c.id, c.account_id, c.provider_calendar_id, c.name, c.color, c.time_zone, c.is_primary,
	c.access_role, c.selected, c.sync_token, c.window_start, c.window_end, c.synced_at`

func scanCalendar(row interface{ Scan(...any) error }) (models.Calendar, error) {
	var c models.Calendar
	var primary, selected int
	var ws, we, sa sql.NullTime
	if err := row.Scan(&c.ID, &c.AccountID, &c.ProviderCalendarID, &c.Name, &c.Color, &c.TimeZone, &primary,
		&c.AccessRole, &selected, &c.SyncToken, &ws, &we, &sa); err != nil {
		return c, err
	}
	c.IsPrimary, c.Selected = primary == 1, selected == 1
	c.WindowStart, c.WindowEnd, c.SyncedAt = nullTimePtr(ws), nullTimePtr(we), nullTimePtr(sa)
	return c, nil
}

// ListCalendarsForUser lists every calendar of the user's accounts.
func (db *DB) ListCalendarsForUser(ctx context.Context, userID string) ([]models.Calendar, error) {
	rows, err := db.Read().QueryContext(ctx, `
		SELECT `+calendarColumns+`
		FROM calendars c JOIN accounts a ON a.id = c.account_id
		WHERE a.user_id = ? AND COALESCE(a.is_deleting, 0) = 0
		ORDER BY a.email_address COLLATE NOCASE, c.is_primary DESC, c.name COLLATE NOCASE, c.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Calendar
	for rows.Next() {
		c, err := scanCalendar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListSelectedCalendarsForAccount returns the calendars sync should fetch.
func (db *DB) ListSelectedCalendarsForAccount(ctx context.Context, accountID string) ([]models.Calendar, error) {
	rows, err := db.Read().QueryContext(ctx, `
		SELECT `+calendarColumns+` FROM calendars c
		WHERE c.account_id = ? AND c.selected = 1 ORDER BY c.id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Calendar
	for rows.Next() {
		c, err := scanCalendar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetCalendarSelected toggles a calendar the user owns; foreign and missing
// calendars are indistinguishable.
func (db *DB) SetCalendarSelected(ctx context.Context, userID string, calendarID int64, selected bool) error {
	res, err := db.Write().ExecContext(ctx, `
		UPDATE calendars SET selected = ?
		WHERE id = ? AND account_id IN (SELECT id FROM accounts WHERE user_id = ? AND COALESCE(is_deleting, 0) = 0)`,
		boolInt(selected), calendarID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrCalendarNotFound
	}
	return nil
}

func upsertCalendarEventTx(ctx context.Context, tx *sql.Tx, calendarID int64, accountID string, e models.CalendarEvent) error {
	attendees, err := json.Marshal(e.Attendees)
	if err != nil {
		return err
	}
	if e.Attendees == nil {
		attendees = []byte("[]")
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO calendar_events (
			calendar_id, account_id, provider_event_id, ical_uid, recurring_event_id, title, location, description,
			start_at, end_at, all_day, start_date, end_date, event_time_zone, status, organizer_email,
			self_response, attendees, html_link, meeting_url, updated_at_provider)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(calendar_id, provider_event_id) DO UPDATE SET
			ical_uid = excluded.ical_uid, recurring_event_id = excluded.recurring_event_id, title = excluded.title,
			location = excluded.location, description = excluded.description, start_at = excluded.start_at,
			end_at = excluded.end_at, all_day = excluded.all_day, start_date = excluded.start_date,
			end_date = excluded.end_date, event_time_zone = excluded.event_time_zone, status = excluded.status,
			organizer_email = excluded.organizer_email, self_response = excluded.self_response,
			attendees = excluded.attendees, html_link = excluded.html_link, meeting_url = excluded.meeting_url,
			updated_at_provider = excluded.updated_at_provider`,
		calendarID, accountID, e.ProviderEventID, e.ICalUID, e.RecurringEventID, e.Title, e.Location, e.Description,
		formatDBTime(e.StartAt), formatDBTime(e.EndAt), boolInt(e.AllDay), e.StartDate, e.EndDate, e.EventTimeZone,
		e.Status, e.OrganizerEmail, e.SelfResponse, string(attendees), e.HTMLLink, e.MeetingURL, nullableDBTime(e.UpdatedAtProvider))
	return err
}

// ApplyCalendarEventPage applies one incremental page atomically.
func (db *DB) ApplyCalendarEventPage(ctx context.Context, calendarID int64, accountID string, upserts []models.CalendarEvent, deleteIDs []string) error {
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, e := range upserts {
		if err := upsertCalendarEventTx(ctx, tx, calendarID, accountID, e); err != nil {
			return fmt.Errorf("upsert event %s: %w", e.ProviderEventID, err)
		}
	}
	for _, id := range deleteIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM calendar_events WHERE calendar_id = ? AND provider_event_id = ?`, calendarID, id); err != nil {
			return fmt.Errorf("delete event %s: %w", id, err)
		}
	}
	return tx.Commit()
}

// ReplaceCalendarEvents is the full-sync commit: the calendar's events become
// exactly `events`, and the new sync token and window are stored, in one
// transaction so a failed resync never leaves a half-empty calendar.
func (db *DB) ReplaceCalendarEvents(ctx context.Context, calendarID int64, accountID string, events []models.CalendarEvent, syncToken string, windowStart, windowEnd time.Time) error {
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM calendar_events WHERE calendar_id = ?`, calendarID); err != nil {
		return err
	}
	for _, e := range events {
		if err := upsertCalendarEventTx(ctx, tx, calendarID, accountID, e); err != nil {
			return fmt.Errorf("insert event %s: %w", e.ProviderEventID, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE calendars SET sync_token = ?, window_start = ?, window_end = ?, synced_at = ? WHERE id = ?`,
		syncToken, formatDBTime(windowStart), formatDBTime(windowEnd), formatDBTime(time.Now()), calendarID); err != nil {
		return err
	}
	return tx.Commit()
}

// FinishIncrementalCalendarSync stores the new token and prunes rows that fell
// outside the calendar's window, so incremental results (which Google does not
// guarantee to honour the original timeMin/timeMax) cannot grow the table
// without bound. Past events end before window_start; far-future ones start
// at/after window_end.
func (db *DB) FinishIncrementalCalendarSync(ctx context.Context, calendarID int64, syncToken string) error {
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM calendar_events WHERE calendar_id = ? AND (
			end_at <= (SELECT window_start FROM calendars WHERE id = ?) OR
			start_at >= (SELECT window_end FROM calendars WHERE id = ?))`, calendarID, calendarID, calendarID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE calendars SET sync_token = ?, synced_at = ? WHERE id = ?`,
		syncToken, formatDBTime(time.Now()), calendarID); err != nil {
		return err
	}
	return tx.Commit()
}

// ListCalendarEvents returns events of the user's selected calendars that
// overlap [start, end). Timed events overlap when start_at < end AND
// end_at > start (a zero-length event overlaps when it starts inside the
// range). All-day events are floating dates, so they are compared by date in
// loc rather than by their UTC proxy instants.
func (db *DB) ListCalendarEvents(ctx context.Context, userID string, start, end time.Time, loc *time.Location) ([]models.CalendarEventView, error) {
	if loc == nil {
		loc = time.UTC
	}
	startDate := start.In(loc).Format(calendarDateLayout)
	endDateExcl := end.Add(-time.Nanosecond).In(loc).AddDate(0, 0, 1).Format(calendarDateLayout)
	rows, err := db.Read().QueryContext(ctx, `
		SELECT e.id, e.calendar_id, e.account_id, e.provider_event_id, e.ical_uid, e.recurring_event_id, e.title,
		       e.location, e.description, e.start_at, e.end_at, e.all_day, e.start_date, e.end_date,
		       e.event_time_zone, e.status, e.organizer_email, e.self_response, e.attendees, e.html_link,
		       e.meeting_url, e.updated_at_provider, c.name, c.color
		FROM calendar_events e
		JOIN calendars c ON c.id = e.calendar_id
		JOIN accounts a ON a.id = c.account_id
		WHERE a.user_id = ? AND COALESCE(a.is_deleting, 0) = 0 AND c.selected = 1
		  AND ((e.all_day = 0 AND e.start_at < ? AND (e.end_at > ? OR (e.end_at = e.start_at AND e.start_at >= ?)))
		    OR (e.all_day = 1 AND e.start_date < ? AND e.end_date > ?))
		ORDER BY e.all_day DESC, e.start_at, e.id`,
		userID, formatDBTime(end), formatDBTime(start), formatDBTime(start), endDateExcl, startDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.CalendarEventView{}
	for rows.Next() {
		var v models.CalendarEventView
		var allDay int
		var attendees string
		var updated sql.NullTime
		if err := rows.Scan(&v.ID, &v.CalendarID, &v.AccountID, &v.ProviderEventID, &v.ICalUID, &v.RecurringEventID, &v.Title,
			&v.Location, &v.Description, &v.StartAt, &v.EndAt, &allDay, &v.StartDate, &v.EndDate,
			&v.EventTimeZone, &v.Status, &v.OrganizerEmail, &v.SelfResponse, &attendees, &v.HTMLLink,
			&v.MeetingURL, &updated, &v.CalendarName, &v.CalendarColor); err != nil {
			return nil, err
		}
		v.AllDay = allDay == 1
		v.StartAt, v.EndAt = v.StartAt.UTC(), v.EndAt.UTC()
		v.UpdatedAtProvider = nullTimePtr(updated)
		if err := json.Unmarshal([]byte(attendees), &v.Attendees); err != nil || v.Attendees == nil {
			v.Attendees = []models.EventAttendee{}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SetCalendarAccountState records whether the account lacks calendar access.
// A successful sync passes needsReconnect=false and an empty message.
func (db *DB) SetCalendarAccountState(ctx context.Context, accountID string, needsReconnect bool, lastError string) error {
	synced := any(nil)
	if !needsReconnect && lastError == "" {
		synced = formatDBTime(time.Now())
	}
	_, err := db.Write().ExecContext(ctx, `
		INSERT INTO calendar_account_state (account_id, needs_reconnect, last_error, last_synced_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(account_id) DO UPDATE SET needs_reconnect = excluded.needs_reconnect,
			last_error = excluded.last_error,
			last_synced_at = COALESCE(excluded.last_synced_at, calendar_account_state.last_synced_at)`,
		accountID, boolInt(needsReconnect), lastError, synced)
	return err
}

// ListCalendarAccountStates returns the sync state keyed by the user's account ids.
func (db *DB) ListCalendarAccountStates(ctx context.Context, userID string) (map[string]models.CalendarAccountState, error) {
	rows, err := db.Read().QueryContext(ctx, `
		SELECT s.account_id, s.needs_reconnect, s.last_error, s.last_synced_at
		FROM calendar_account_state s JOIN accounts a ON a.id = s.account_id
		WHERE a.user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]models.CalendarAccountState{}
	for rows.Next() {
		var id string
		var st models.CalendarAccountState
		var reconnect int
		var synced sql.NullTime
		if err := rows.Scan(&id, &reconnect, &st.LastError, &synced); err != nil {
			return nil, err
		}
		st.NeedsReconnect, st.LastSyncedAt = reconnect == 1, nullTimePtr(synced)
		out[id] = st
	}
	return out, rows.Err()
}

// CalendarAccount is an account that can have calendars.
type CalendarAccount struct {
	ID    string
	Email string
}

// ListCalendarAccounts lists the user's accounts whose provider supports
// calendar sync (Google only for now).
func (db *DB) ListCalendarAccounts(ctx context.Context, userID string) ([]CalendarAccount, error) {
	rows, err := db.Read().QueryContext(ctx, `
		SELECT id, email_address FROM accounts
		WHERE user_id = ? AND provider = 'gmail' AND COALESCE(is_deleting, 0) = 0
		ORDER BY email_address COLLATE NOCASE`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalendarAccount
	for rows.Next() {
		var a CalendarAccount
		if err := rows.Scan(&a.ID, &a.Email); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
