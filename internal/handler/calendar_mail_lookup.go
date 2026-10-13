package handler

import (
	"context"
	"strings"
	"time"

	"github.com/fahadjibransheikh/raven/internal/storage"
)

// Invitations often reach the mailbox before Raven's periodic calendar window
// (a month back to three months ahead) has cached them, or describe an event
// outside that window. The response form needs a cached event, so an
// unmatched invitation is looked up at the provider by syncing a narrow window
// around its start instead of waiting for the owner to press Refresh.
const mailInvitationLookupCooldown = 2 * time.Minute

// mailInvitationNeedsLookup reports whether an unmatched item is an active
// invitation addressed to this account, i.e. one the owner could answer.
func mailInvitationNeedsLookup(item mailCalendarEvent) bool {
	return item.Method == "REQUEST" && item.Invited && !item.Cancelled && item.Organizer != "" && !(item.HasRecurrence && item.Recurrence == nil)
}

// mailInvitationWindow is a day either side of the occurrence the email names.
func mailInvitationWindow(item mailCalendarEvent) (time.Time, time.Time, bool) {
	var start, end time.Time
	switch {
	case item.Recurrence != nil && !item.AllDay:
		start, end = *item.Recurrence, *item.Recurrence
	case item.AllDay && item.StartDate != "":
		day, err := time.Parse("2006-01-02", item.StartDate)
		if err != nil {
			return start, end, false
		}
		start, end = day, day.AddDate(0, 0, 1)
	case item.Start != nil:
		start, end = *item.Start, *item.Start
		if item.End != nil && item.End.After(end) {
			end = *item.End
		}
	default:
		return start, end, false
	}
	// A meeting that is over has nothing left to answer; a series may still run.
	if end.AddDate(0, 0, 1).Before(time.Now()) && !item.Recurring {
		return start, end, false
	}
	return start.AddDate(0, 0, -1), end.AddDate(0, 0, 1), true
}

// syncMailInvitation refreshes the invitation's window for its account and
// reports whether it ran. Sources that synced moments ago are not asked again,
// so reopening an email whose event truly is not on the calendar stays cheap.
func (h *Handler) syncMailInvitation(ctx context.Context, accountID string, item mailCalendarEvent) bool {
	start, end, ok := mailInvitationWindow(item)
	if !ok {
		return false
	}
	sources, err := h.db.ListSelectedCalendarSources(ctx, h.userID(ctx))
	if err != nil {
		return false
	}
	fresh, any := true, false
	for _, source := range sources {
		if source.AccountID != accountID {
			continue
		}
		any = true
		if source.LastSuccessAt == nil || time.Since(*source.LastSuccessAt) > mailInvitationLookupCooldown {
			fresh = false
		}
	}
	if !any || fresh {
		return false
	}
	_, _ = h.syncCalendarScopedWindow(ctx, h.userID(ctx), start, end, "", accountID)
	return true
}

// mailAccountHasSelectedCalendar lets the card say why an invitation cannot be
// answered when the account has no calendar for Raven to look in.
func (h *Handler) mailAccountHasSelectedCalendar(ctx context.Context, accountID string) bool {
	sources, err := h.db.ListSelectedCalendarSources(ctx, h.userID(ctx))
	if err != nil {
		return true
	}
	for _, source := range sources {
		if source.AccountID == accountID {
			return true
		}
	}
	return false
}

// mailCalendarMovedOccurrence matches an occurrence that was rescheduled after
// the series began. Google keeps the original start in the instance ID.
func mailCalendarMovedOccurrence(event storage.CalendarEvent, recurrence time.Time) bool {
	return strings.HasSuffix(event.RemoteID, "_"+recurrence.UTC().Format("20060102T150405Z"))
}
