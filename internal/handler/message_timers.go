package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fahadjibransheikh/raven/internal/mail"
	"github.com/fahadjibransheikh/raven/internal/storage"
)

const (
	messageTimerTick      = 30 * time.Second
	messageTimerMaxAhead  = 366 * 24 * time.Hour
	messageTimerBatchSize = 100
)

// handleSetMessageTimer snoozes a thread or sets a follow-up reminder on it.
// The body is JSON {"until": RFC 3339 time}; DELETE clears the timer.
func (h *Handler) handleSetMessageTimer(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		msgID, info, err := h.getMessageInfo(ctx, r.PathValue("id"))
		if err != nil {
			writeMessageTargetError(w, r, err)
			return
		}
		if r.Method == http.MethodDelete {
			cleared, err := h.db.ClearMessageTimer(ctx, h.userID(ctx), msgID, kind)
			if err != nil {
				http.Error(w, "could not clear the reminder", http.StatusInternalServerError)
				return
			}
			h.publishTimerMutation(ctx, info.AccountID)
			writeJSON(w, map[string]bool{"cleared": cleared})
			return
		}
		var body struct {
			Until string `json:"until"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		until, err := time.Parse(time.RFC3339, strings.TrimSpace(body.Until))
		now := time.Now().UTC()
		if err != nil || !until.After(now) || until.Sub(now) > messageTimerMaxAhead {
			http.Error(w, "until must be a future time within a year", http.StatusBadRequest)
			return
		}
		timer, err := h.db.SetMessageTimer(ctx, h.userID(ctx), msgID, kind, until, now)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "could not save the reminder", http.StatusInternalServerError)
			return
		}
		h.publishTimerMutation(ctx, info.AccountID)
		writeJSON(w, map[string]string{"kind": kind, "due_at": timer.DueAt.Format(time.RFC3339)})
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// publishTimerMutation refreshes the inbox lists that a snooze changed.
func (h *Handler) publishTimerMutation(ctx context.Context, accountID string) {
	folderID, _, err := h.db.GetFolderIDByRole(ctx, accountID, "inbox")
	if err != nil {
		folderID = ""
	}
	h.publishMutation(accountID, folderID)
}

// StartMessageTimerWorker wakes snoozed threads and fires follow-up reminders.
func (h *Handler) StartMessageTimerWorker(ctx context.Context) {
	if h.ownedMailbox != nil {
		h.ownedMailbox.StartMessageTimerWorker(ctx)
		return
	}
	go func() {
		ticker := time.NewTicker(messageTimerTick)
		defer ticker.Stop()
		for {
			h.runMessageTimers(ctx, time.Now().UTC())
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (h *Handler) runMessageTimers(ctx context.Context, now time.Time) {
	if h.userStorage == nil {
		if h.syncer == nil {
			return
		}
		h.runMessageTimersIn(ctx, h.db, "", now, h.syncer.Events().Publish)
		return
	}
	// shortcut: opens every owner's store each tick; fine for a handful of users, move to the account service schedule if that grows.
	after := ""
	for ctx.Err() == nil {
		owners, err := h.userStorage.ListStartupStoreOwners(ctx, after, 64)
		if err != nil || len(owners) == 0 {
			if err != nil && ctx.Err() == nil {
				log.Printf("message timers: list owners: %v", err)
			}
			return
		}
		for _, owner := range owners {
			if err := h.userStorage.WithExistingUser(ctx, owner, func(db *storage.DB) error {
				h.runMessageTimersIn(ctx, db, owner, now, h.userIMAP.Events().Publish)
				return nil
			}); err != nil && !errors.Is(err, storage.ErrUserStoreOwner) && ctx.Err() == nil {
				log.Printf("message timers %s: %v", owner, err)
			}
			after = owner
		}
	}
}

func (h *Handler) runMessageTimersIn(ctx context.Context, db *storage.DB, owner string, now time.Time, publish func(mail.Event)) {
	replied, err := db.RepliedSnoozes(ctx, messageTimerBatchSize)
	if err != nil {
		log.Printf("message timers: replied snoozes: %v", err)
	}
	for _, timer := range replied {
		// The reply already raised its own new-mail notification.
		if done, err := db.FinishMessageTimer(ctx, timer); err != nil {
			log.Printf("message timers: end replied snooze %d: %v", timer.ID, err)
		} else if done {
			publishTimerEvents(ctx, db, owner, timer, "", publish)
		}
	}
	due, err := db.DueMessageTimers(ctx, now, messageTimerBatchSize)
	if err != nil {
		log.Printf("message timers: due: %v", err)
		return
	}
	for _, timer := range due {
		prefix := "Snoozed: "
		if timer.Kind == storage.MessageTimerFollowUp {
			prefix = "No reply yet: "
			if replied, err := db.ThreadRepliedSince(ctx, timer.AccountID, timer.ThreadKey, timer.CreatedAt); err != nil {
				log.Printf("message timers: reply check %d: %v", timer.ID, err)
				continue
			} else if replied {
				prefix = ""
			}
		}
		done, err := db.FinishMessageTimer(ctx, timer)
		if err != nil {
			log.Printf("message timers: finish %d: %v", timer.ID, err)
			continue
		}
		if done {
			publishTimerEvents(ctx, db, owner, timer, prefix, publish)
		}
	}
}

// publishTimerEvents refreshes the inbox and, when notice is set, raises a
// new-mail notification for the thread's latest message.
func publishTimerEvents(ctx context.Context, db *storage.DB, owner string, timer storage.MessageTimer, notice string, publish func(mail.Event)) {
	folderID, _, _ := db.GetFolderIDByRole(ctx, timer.AccountID, "inbox")
	publish(mail.Event{Type: mail.EventMutation, UserID: owner, AccountID: timer.AccountID, FolderID: folderID})
	if notice == "" {
		return
	}
	var subject, fromName, fromEmail string
	if err := db.Read().QueryRowContext(ctx, `SELECT subject, from_name, from_email FROM messages WHERE id = ?`, timer.MessageID).Scan(&subject, &fromName, &fromEmail); err != nil {
		log.Printf("message timers: load message %s: %v", strconv.FormatInt(timer.MessageID, 10), err)
		return
	}
	publish(mail.Event{
		Type: mail.EventNewMail, UserID: owner, AccountID: timer.AccountID, FolderID: folderID, FolderRole: "inbox",
		Payload: map[string]any{"count": 1, "unread_count": 1, "folder_name": "Inbox", "subject": notice + subject, "from_name": fromName, "from_email": fromEmail},
	})
}
