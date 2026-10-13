package mail

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fahadjibransheikh/raven/internal/mail/imap"
	"github.com/fahadjibransheikh/raven/internal/storage"
)

// Messages synced before sizes were stored have none, so each sync looks up a
// limited number of them, newest first. Gmail charges 5 quota units per lookup
// against a limit of 6,000 units a minute per mailbox, shared with syncing and
// reading mail, so size lookups stay at a fifth of it. Outlook allows 10,000
// requests per 10 minutes per mailbox.
const (
	imapSizeBackfillPerFolder  = 5000
	gmailSizeBackfillPerSync   = 240
	gmailSizeLookupsPerMinute  = 240
	gmailSizeLookupWorkers     = 4
	outlookSizeBackfillPerSync = 4000
	outlookSizeBatchSize       = 20
)

// After the provider refuses a lookup, for example for going over its quota,
// size lookups for that account wait this long instead of trying again on the
// next sync.
const sizeLookupPause = 15 * time.Minute

var sizeLookupsPausedUntil sync.Map // account ID -> time.Time

func sizeLookupsPaused(accountID string) bool {
	until, ok := sizeLookupsPausedUntil.Load(accountID)
	return ok && time.Now().Before(until.(time.Time))
}

func pauseSizeLookups(accountID string, err error) {
	if status, ok := providerAPIStatus(err); ok && (status == http.StatusForbidden || status == http.StatusTooManyRequests || status >= http.StatusInternalServerError) {
		sizeLookupsPausedUntil.Store(accountID, time.Now().Add(sizeLookupPause))
	}
}

// Outlook reports the message size only as this MAPI property.
const outlookMessageSizeProperty = "Integer 0x0E08"

func (o *SyncOrchestrator) backfillIMAPFolderSizes(ctx context.Context, client *imap.Client, folder storage.FolderSyncInfo, expectedUIDValidity uint32) {
	uids, err := o.imapRepository().ListFolderUIDsMissingSize(ctx, folder.ID, imapSizeBackfillPerFolder)
	if err != nil || len(uids) == 0 {
		return
	}
	sizes, err := client.FetchSizes(ctx, folder.RemoteID, uids, expectedUIDValidity)
	if err != nil {
		log.Printf("fetch message sizes %s/%s: %v", folder.AccountID, folder.RemoteID, err)
		return
	}
	if err := o.imapRepository().SetFolderMessageSizes(ctx, folder.ID, sizes); err != nil {
		log.Printf("save message sizes %s/%s: %v", folder.AccountID, folder.RemoteID, err)
	}
}

func (o *SyncOrchestrator) backfillGmailMessageSizes(ctx context.Context, accountID string) {
	if sizeLookupsPaused(accountID) {
		return
	}
	ids, err := o.gmailRepository().ListProviderMessagesMissingSize(ctx, accountID, gmailSizeBackfillPerSync)
	if err != nil || len(ids) == 0 {
		return
	}
	token, err := o.tokenProvider.GetOAuthTokenForAccount(ctx, accountID)
	if err != nil {
		return
	}
	limiter := newGmailAPIMetadataLimiter(gmailSizeLookupsPerMinute, gmailSizeLookupWorkers)
	defer limiter.Stop()
	lookupCtx, stop := context.WithCancel(ctx)
	defer stop()
	jobs := make(chan string)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sizes := make(map[string]int64, len(ids))
	for range gmailSizeLookupWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				if limiter.Wait(lookupCtx) != nil {
					return
				}
				size, err := getGmailAPIMessageSize(lookupCtx, token, id)
				if providerMessageNotFound(err) {
					size, err = storage.MessageSizeUnavailable, nil
				}
				if err != nil {
					if lookupCtx.Err() == nil {
						log.Printf("gmail message size account=%s: %v", accountID, err)
						pauseSizeLookups(accountID, err)
					}
					// Stop on the first failure, such as a rate limit; the
					// next sync continues.
					stop()
					return
				}
				if size <= 0 {
					size = storage.MessageSizeUnavailable
				}
				mu.Lock()
				sizes[id] = size
				mu.Unlock()
			}
		}()
	}
feed:
	for _, id := range ids {
		select {
		case jobs <- id:
		case <-lookupCtx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if err := o.gmailRepository().SetProviderMessageSizes(ctx, accountID, sizes); err != nil {
		log.Printf("save gmail message sizes account=%s: %v", accountID, err)
	}
}

func getGmailAPIMessageSize(ctx context.Context, token, providerMessageID string) (int64, error) {
	values := url.Values{}
	values.Set("format", "minimal")
	values.Set("fields", "id,sizeEstimate")
	endpoint := gmailAPIBaseURL + "/users/me/messages/" + url.PathEscape(strings.TrimSpace(providerMessageID)) + "?" + values.Encode()
	var msg gmailAPIMessage
	if err := providerJSON(ctx, http.MethodGet, endpoint, token, nil, nil, &msg); err != nil {
		return 0, err
	}
	return msg.SizeEstimate, nil
}

func (o *SyncOrchestrator) backfillOutlookMessageSizes(ctx context.Context, accountID string) {
	graphTokens, ok := o.tokenProvider.(graphMailTokenProvider)
	if !ok || sizeLookupsPaused(accountID) {
		return
	}
	ids, err := o.outlookRepository().ListProviderMessagesMissingSize(ctx, accountID, outlookSizeBackfillPerSync)
	if err != nil || len(ids) == 0 {
		return
	}
	token, err := graphTokens.GetMicrosoftGraphMailTokenForAccount(ctx, accountID)
	if err != nil {
		return
	}
	sizes := make(map[string]int64, len(ids))
	for start := 0; start < len(ids); start += outlookSizeBatchSize {
		chunk := ids[start:min(start+outlookSizeBatchSize, len(ids))]
		if err := fetchOutlookMessageSizes(ctx, token, chunk, sizes); err != nil {
			log.Printf("outlook message sizes account=%s: %v", accountID, err)
			pauseSizeLookups(accountID, err)
			break
		}
	}
	if err := o.outlookRepository().SetProviderMessageSizes(ctx, accountID, sizes); err != nil {
		log.Printf("save outlook message sizes account=%s: %v", accountID, err)
	}
}

func fetchOutlookMessageSizes(ctx context.Context, token string, ids []string, sizes map[string]int64) error {
	query := "?$select=id&$expand=" + url.QueryEscape("singleValueExtendedProperties($filter=id eq '"+outlookMessageSizeProperty+"')")
	requests := make([]providerBatchRequest, 0, len(ids))
	for i, id := range ids {
		requests = append(requests, providerBatchRequest{
			ID:      strconv.Itoa(i),
			Method:  http.MethodGet,
			URL:     "/me/messages/" + url.PathEscape(id) + query,
			Headers: outlookImmutableIDHeaders(),
		})
	}
	var envelope providerBatchResponseEnvelope
	if err := providerJSON(ctx, http.MethodPost, outlookGraphBaseURL+"/$batch", token, nil, map[string][]providerBatchRequest{"requests": requests}, &envelope); err != nil {
		return err
	}
	for _, response := range envelope.Responses {
		i, err := strconv.Atoi(response.ID)
		if err != nil || i < 0 || i >= len(ids) {
			continue
		}
		if response.Status == http.StatusNotFound {
			sizes[ids[i]] = storage.MessageSizeUnavailable
			continue
		}
		if response.Status < 200 || response.Status >= 300 {
			if response.Status == http.StatusUnauthorized || response.Status == http.StatusForbidden ||
				response.Status == http.StatusTooManyRequests || response.Status >= http.StatusInternalServerError {
				return &providerAPIError{StatusCode: response.Status, Body: strings.TrimSpace(string(response.Body))}
			}
			continue
		}
		sizes[ids[i]] = outlookMessageSizeFromResponse(response.Body)
	}
	return nil
}

func outlookMessageSizeFromResponse(raw json.RawMessage) int64 {
	var body struct {
		Properties []struct {
			ID    string `json:"id"`
			Value string `json:"value"`
		} `json:"singleValueExtendedProperties"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return storage.MessageSizeUnavailable
	}
	for _, property := range body.Properties {
		size, err := strconv.ParseInt(strings.TrimSpace(property.Value), 10, 64)
		if err == nil && size > 0 {
			return size
		}
	}
	return storage.MessageSizeUnavailable
}
