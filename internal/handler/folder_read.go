package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	goimap "github.com/emersion/go-imap/v2"

	"github.com/cristianadrielbraun/gofer/internal/mail/imap"
	"github.com/cristianadrielbraun/gofer/internal/models"
	"github.com/cristianadrielbraun/gofer/internal/storage"
)

// A folder_read job pages through the whole folder, so the per-message
// two-minute budget is far too small for a large mailbox. The job is
// idempotent (it only touches what is still unread), so hitting this limit
// just resumes on the next retry.
const folderReadTimeout = 20 * time.Minute

// While a job is queued, sync treats every message at or below its cutoff as
// read, so a job that can never succeed (revoked auth, deleted label) would pin
// the folder read forever. The per-message worker has no attempt limit; for
// folder jobs 8 attempts spans about an hour with the existing backoff
// (30s doubling to 32m), enough for outages, then the job is dropped and sync
// reconciles from the server.
const folderReadMaxAttempts = 8

const (
	gmailListPageSize    = 500  // users.messages.list maxResults ceiling
	gmailBatchModifyMax  = 1000 // users.messages.batchModify ids limit
	outlookBatchMax      = 20   // Graph JSON batching request limit
	outlookListPageSize  = 100
	outlookBatchPatchHdr = `IdType="ImmutableId"`
)

// folderReadIMAPClient is the subset of the IMAP client the folder worker uses.
type folderReadIMAPClient interface {
	AddFlagsUIDRangeIfUIDValidity(ctx context.Context, folderRemoteName string, maxUID uint32, expectedUIDValidity uint32, flags []goimap.Flag) (bool, error)
	Close() error
}

func (h *Handler) handleMarkFolderRead(w http.ResponseWriter, r *http.Request) {
	cutoff := time.Now().UTC()
	ctx := context.WithoutCancel(r.Context())
	userID := h.userID(ctx)
	folderID, err := h.db.ResolveFolderIDForUser(ctx, userID, strings.TrimSpace(r.PathValue("id")))
	if err == nil && folderID == "" {
		err = sql.ErrNoRows
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "folder not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to resolve folder", http.StatusInternalServerError)
		return
	}
	results, err := h.db.MarkFolderReadAndQueueForUser(ctx, userID, folderID, cutoff)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, "folder not found", http.StatusNotFound)
		return
	case errors.Is(err, storage.ErrFolderReadUnsupported):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	updated, folders := 0, 0
	skipped := []string{} // accounts whose folder has no provider identity; the client names them in a toast
	for _, result := range results {
		if result.Skipped {
			skipped = append(skipped, result.AccountName)
			continue
		}
		updated += result.Marked
		folders++
		h.publishMutation(result.AccountID, result.FolderID)
	}
	h.signalMessageMutationWorker()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"updated": updated, "folders": folders, "skipped": skipped})
}

// runDueFolderReads processes queued folder_read jobs and reports whether any
// were claimed.
func (h *Handler) runDueFolderReads(ctx context.Context) bool {
	jobs, err := h.db.ClaimDueFolderReadMutations(ctx, time.Now(), 5)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("folder-read: claim operations: %v", err)
		}
		return false
	}
	for _, job := range jobs {
		h.applyQueuedFolderRead(ctx, job)
	}
	return len(jobs) > 0
}

func (h *Handler) applyQueuedFolderRead(parent context.Context, job storage.FolderReadMutation) {
	ctx, cancel := context.WithTimeout(parent, folderReadTimeout)
	defer cancel()
	err := h.applyRemoteFolderRead(ctx, job)
	if err != nil && job.AttemptCount >= folderReadMaxAttempts {
		log.Printf("folder-read: giving up account=%s folder=%s after %d attempts: %v", job.AccountID, job.FolderID, job.AttemptCount, err)
		if dbErr := h.db.CompleteFolderReadMutation(context.Background(), job.ID); dbErr != nil {
			log.Printf("folder-read: drop abandoned job id=%s: %v", job.ID, dbErr)
		}
		return
	}
	if err != nil {
		nextAttempt := time.Now().Add(sentCopyRetryDelay(job.AttemptCount))
		if dbErr := h.db.FinishFolderReadMutationWithError(context.Background(), job.ID, err.Error(), nextAttempt); dbErr != nil {
			log.Printf("folder-read: save failure id=%s: %v", job.ID, dbErr)
			return
		}
		log.Printf("folder-read: folder=%s failed; retry at %s: %v", job.FolderID, nextAttempt.Format(time.RFC3339), err)
		return
	}
	if err := h.db.CompleteFolderReadMutation(context.Background(), job.ID); err != nil {
		log.Printf("folder-read: mark applied id=%s: %v", job.ID, err)
	}
}

func (h *Handler) applyRemoteFolderRead(ctx context.Context, job storage.FolderReadMutation) error {
	remoteID, providerRemoteID, _, err := h.db.GetFolderReadRemoteInfo(ctx, job.FolderID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // folder was removed; nothing left to mark
	}
	if err != nil {
		return err
	}
	switch job.ProviderType {
	case storage.MessageMutationProviderGmail:
		if h.mailCredentials() == nil {
			return fmt.Errorf("Gmail authentication is not available")
		}
		token, err := h.mailCredentials().GetOAuthTokenForAccount(ctx, job.AccountID)
		if err != nil {
			return fmt.Errorf("get Gmail token: %w", err)
		}
		if providerRemoteID == "" {
			return fmt.Errorf("Gmail folder has no label identity")
		}
		return h.markGmailLabelRead(ctx, token, providerRemoteID, job.CutoffAt)
	case storage.MessageMutationProviderOutlook:
		if h.mailCredentials() == nil {
			return fmt.Errorf("Outlook authentication is not available")
		}
		token, err := h.mailCredentials().GetMicrosoftGraphMailTokenForAccount(ctx, job.AccountID)
		if err != nil {
			return fmt.Errorf("get Outlook token: %w", err)
		}
		if providerRemoteID == "" {
			return fmt.Errorf("Outlook folder has no provider identity")
		}
		return h.markOutlookFolderRead(ctx, token, providerRemoteID, job.CutoffAt)
	case storage.MessageMutationProviderIMAP:
		if remoteID == "" {
			return fmt.Errorf("IMAP folder has no remote identity")
		}
		if h.accountStore == nil {
			return fmt.Errorf("IMAP account storage is not available")
		}
		cfg, err := h.accountStore.GetConfig(ctx, job.AccountID)
		if err != nil {
			return err
		}
		password, err := h.resolvePassword(ctx, cfg, job.AccountID)
		if err != nil {
			return err
		}
		factory := h.folderReadIMAPFactory
		if factory == nil {
			factory = func(ctx context.Context, cfg *models.AccountConfig, password string) (folderReadIMAPClient, error) {
				return imap.NewClient(ctx, cfg, password)
			}
		}
		client, err := factory(ctx, cfg, password)
		if err != nil {
			return err
		}
		defer client.Close()
		changed, err := client.AddFlagsUIDRangeIfUIDValidity(ctx, remoteID, job.MaxUID, job.UIDValidity, []goimap.Flag{goimap.FlagSeen})
		if err != nil {
			return err
		}
		if changed {
			return fmt.Errorf("IMAP UIDVALIDITY changed; waiting for folder resync before marking read")
		}
		return nil
	default:
		return fmt.Errorf("unsupported folder read provider %q", job.ProviderType)
	}
}

// markGmailLabelRead lists unread messages carrying the label that arrived
// before the cutoff (users.messages.list, labelIds + q; the ARCHIVE
// pseudo-folder uses a q-only search) and removes UNREAD in
// chunks of 1000 (users.messages.batchModify).
func (h *Handler) markGmailLabelRead(ctx context.Context, token, labelID string, cutoff time.Time) error {
	var ids []string
	pageToken := ""
	for {
		query := url.Values{}
		// before: takes epoch seconds for an exact (timezone-free) instant:
		// https://developers.google.com/workspace/gmail/api/guides/filtering
		before := " before:" + strconv.FormatInt(cutoff.Unix(), 10)
		if labelID == "ARCHIVE" {
			// Gmail has no archive label; this mirrors the sync definition
			// (gmailAPIMessageIsArchived / listGmailAPIMessageIDPage), so
			// the remote set matches the local ARCHIVE folder's messages.
			query.Set("q", "is:unread -in:inbox -in:sent -in:drafts -in:spam -in:trash"+before)
		} else {
			query.Set("labelIds", labelID)
			query.Set("q", "is:unread"+before)
		}
		query.Set("maxResults", strconv.Itoa(gmailListPageSize))
		query.Set("includeSpamTrash", "true") // otherwise SPAM and TRASH labels list nothing
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		var page struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := doGoogleJSON(ctx, http.MethodGet, gmailAPIBaseURL+"/users/me/messages?"+query.Encode(), token, nil, &page); err != nil {
			return fmt.Errorf("list unread Gmail messages: %w", err)
		}
		for _, m := range page.Messages {
			ids = append(ids, m.ID)
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	for start := 0; start < len(ids); start += gmailBatchModifyMax {
		end := min(start+gmailBatchModifyMax, len(ids))
		payload := map[string][]string{"ids": ids[start:end], "removeLabelIds": {"UNREAD"}}
		if err := doGoogleJSON(ctx, http.MethodPost, gmailAPIBaseURL+"/users/me/messages/batchModify", token, payload, nil); err != nil {
			return fmt.Errorf("batch mark Gmail messages read: %w", err)
		}
	}
	return nil
}

type outlookBatchRequest struct {
	ID      string            `json:"id"`
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    map[string]bool   `json:"body"`
}

// markOutlookFolderRead pages the folder's unread messages received at or
// before the cutoff and PATCHes isRead via JSON batching (20 per $batch).
func (h *Handler) markOutlookFolderRead(ctx context.Context, token, folderProviderID string, cutoff time.Time) error {
	filter := "isRead eq false and receivedDateTime le " + cutoff.UTC().Format("2006-01-02T15:04:05Z")
	next := outlookGraphBaseURL + "/me/mailFolders/" + url.PathEscape(folderProviderID) + "/messages?" +
		strings.ReplaceAll(url.Values{"$filter": {filter}, "$select": {"id"}, "$top": {strconv.Itoa(outlookListPageSize)}}.Encode(), "+", "%20")
	var ids []string
	for next != "" {
		var page struct {
			Value []struct {
				ID string `json:"id"`
			} `json:"value"`
			NextLink string `json:"@odata.nextLink"`
		}
		if err := h.doOutlookJSON(ctx, http.MethodGet, next, token, nil, &page); err != nil {
			return fmt.Errorf("list unread Outlook messages: %w", err)
		}
		for _, m := range page.Value {
			ids = append(ids, m.ID)
		}
		next = page.NextLink
	}
	var failures []string
	for start := 0; start < len(ids); start += outlookBatchMax {
		end := min(start+outlookBatchMax, len(ids))
		requests := make([]outlookBatchRequest, 0, end-start)
		for i, id := range ids[start:end] {
			requests = append(requests, outlookBatchRequest{
				ID: strconv.Itoa(i + 1), Method: http.MethodPatch, URL: "/me/messages/" + url.PathEscape(id),
				Headers: map[string]string{"Content-Type": "application/json", "Prefer": outlookBatchPatchHdr},
				Body:    map[string]bool{"isRead": true},
			})
		}
		var response struct {
			Responses []struct {
				ID     string `json:"id"`
				Status int    `json:"status"`
			} `json:"responses"`
		}
		if err := h.doOutlookJSON(ctx, http.MethodPost, outlookGraphBaseURL+"/$batch", token, map[string]any{"requests": requests}, &response); err != nil {
			return fmt.Errorf("batch mark Outlook messages read: %w", err)
		}
		answered := 0
		for _, item := range response.Responses {
			answered++
			// 404: deleted or moved since listing; nothing left to mark.
			if (item.Status < 200 || item.Status >= 300) && item.Status != http.StatusNotFound {
				failures = append(failures, strconv.Itoa(item.Status))
			}
		}
		if answered != len(requests) {
			failures = append(failures, "missing")
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d Outlook read updates failed (statuses %s); retrying remaining unread mail", len(failures), strings.Join(failures[:min(len(failures), 5)], ","))
	}
	return nil
}
