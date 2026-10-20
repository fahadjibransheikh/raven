package handler

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/fahadjibransheikh/raven/internal/calendar"
	"github.com/fahadjibransheikh/raven/internal/mail"
	"github.com/fahadjibransheikh/raven/internal/storage"
)

const (
	calendarReminderMaxLead  = 15 * time.Minute
	calendarReminderBatch    = 200
	calendarReminderKeepPast = 24 * time.Hour
)

// calendarReminderLead is how long before a timed event starts Raven notifies
// the user; 0 means off. Unknown values fall back to the 10 minute default:
// MergeUISettings stores any string. Providers' own per-event reminder times
// are not cached, so this one setting applies to every event.
func calendarReminderLead(settings map[string]string) time.Duration {
	switch v := settings["calendar_reminder_minutes"]; v {
	case "0", "5", "10", "15":
		n, _ := strconv.Atoi(v)
		return time.Duration(n) * time.Minute
	default:
		return 10 * time.Minute
	}
}

// runCalendarRemindersIn raises a "starts soon" notification for each timed
// event in db that is inside its owner's lead time, once per occurrence. It
// runs on the message timer tick, per store, so single-user and managed mode
// share it. An event that already started is never announced, so a server that
// was down through the lead time stays quiet instead of announcing the past.
func runCalendarRemindersIn(ctx context.Context, db *storage.DB, now time.Time, publish func(mail.Event)) {
	upcoming, err := db.UpcomingCalendarReminders(ctx, now, calendarReminderMaxLead, calendarReminderBatch)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("calendar reminders: upcoming: %v", err)
		}
		return
	}
	leads := make(map[string]time.Duration)
	for _, reminder := range upcoming {
		lead, known := leads[reminder.UserID]
		if !known {
			lead = calendarReminderLead(db.GetUISettings(ctx, reminder.UserID))
			leads[reminder.UserID] = lead
		}
		until := reminder.StartAt.Sub(now)
		if lead == 0 || until > lead {
			continue
		}
		claimed, err := db.ClaimCalendarReminder(ctx, reminder, now)
		if err != nil {
			log.Printf("calendar reminders: claim %s: %v", reminder.EventID, err)
			continue
		}
		if !claimed {
			continue
		}
		title, body := calendarReminderText(reminder, until)
		publish(mail.Event{
			Type: mail.EventCalendarReminder, UserID: reminder.UserID, AccountID: reminder.AccountID,
			Payload: map[string]any{
				"event_id": reminder.EventID, "title": title, "body": body,
				"start_at": reminder.StartAt.UTC().Format(time.RFC3339), "url": "/calendar",
				"join_url": calendar.MeetingJoinURLWithDescription(reminder.OnlineMeetingJSON, reminder.Description),
			},
		})
	}
	if err := db.PruneCalendarReminders(ctx, now.Add(-calendarReminderKeepPast)); err != nil && ctx.Err() == nil {
		log.Printf("calendar reminders: prune: %v", err)
	}
}

func calendarReminderText(reminder storage.CalendarReminder, until time.Duration) (string, string) {
	title := strings.TrimSpace(reminder.Summary)
	if title == "" {
		title = "(No title)"
	}
	body := "Starting now"
	switch minutes := int(until.Round(time.Minute) / time.Minute); {
	case minutes == 1:
		body = "Starts in 1 minute"
	case minutes > 1:
		body = "Starts in " + strconv.Itoa(minutes) + " minutes"
	}
	if location := strings.TrimSpace(reminder.Location); location != "" {
		body += " · " + location
	}
	return title, body
}
