package models

import "time"

// Calendar is one provider calendar visible through a connected account.
type Calendar struct {
	ID                 int64      `json:"id"`
	AccountID          string     `json:"account_id"`
	Provider           string     `json:"provider"` // owning account's provider: gmail or outlook
	ProviderCalendarID string     `json:"provider_calendar_id"`
	Name               string     `json:"name"`
	Color              string     `json:"color"`
	TimeZone           string     `json:"time_zone"`
	IsPrimary          bool       `json:"is_primary"`
	AccessRole         string     `json:"access_role"`
	Selected           bool       `json:"selected"`
	SyncToken          string     `json:"-"`
	WindowStart        *time.Time `json:"-"`
	WindowEnd          *time.Time `json:"-"`
	SyncedAt           *time.Time `json:"synced_at,omitempty"`
}

// EventAttendee is one invitee of a calendar event.
type EventAttendee struct {
	Email     string `json:"email"`
	Name      string `json:"name,omitempty"`
	Response  string `json:"response,omitempty"` // needsAction, accepted, declined, tentative
	Organizer bool   `json:"organizer,omitempty"`
	Self      bool   `json:"self,omitempty"`
	Optional  bool   `json:"optional,omitempty"`
	Resource  bool   `json:"resource,omitempty"`
}

// CalendarEvent is one expanded (single) event occurrence. Timed events carry
// StartAt/EndAt as instants; all-day events additionally carry StartDate and
// EndDate ("2006-01-02", EndDate exclusive) which are floating calendar dates
// and must not be shifted by a timezone. For all-day events StartAt/EndAt are
// those dates at 00:00 UTC and are only a sort key.
type CalendarEvent struct {
	ID                int64           `json:"id"`
	CalendarID        int64           `json:"calendar_id"`
	AccountID         string          `json:"account_id"`
	ProviderEventID   string          `json:"provider_event_id"`
	ICalUID           string          `json:"ical_uid,omitempty"`
	RecurringEventID  string          `json:"recurring_event_id,omitempty"`
	Title             string          `json:"title"`
	Location          string          `json:"location,omitempty"`
	Description       string          `json:"description,omitempty"`
	StartAt           time.Time       `json:"start"`
	EndAt             time.Time       `json:"end"`
	AllDay            bool            `json:"all_day"`
	StartDate         string          `json:"start_date,omitempty"`
	EndDate           string          `json:"end_date,omitempty"`
	EventTimeZone     string          `json:"time_zone,omitempty"`
	Status            string          `json:"status"`
	OrganizerEmail    string          `json:"organizer_email,omitempty"`
	SelfResponse      string          `json:"self_response,omitempty"`
	Attendees         []EventAttendee `json:"attendees"`
	HTMLLink          string          `json:"html_link,omitempty"`
	MeetingURL        string          `json:"meeting_url,omitempty"`
	UpdatedAtProvider *time.Time      `json:"-"`
}

// CalendarEventView is a CalendarEvent plus its calendar's display fields.
type CalendarEventView struct {
	CalendarEvent
	CalendarName  string `json:"calendar_name"`
	CalendarColor string `json:"calendar_color"`
}

// CalendarAccountState is the per-account calendar sync state.
type CalendarAccountState struct {
	NeedsReconnect bool
	LastError      string
	LastSyncedAt   *time.Time
}
