package mail

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"github.com/fahadjibransheikh/raven/internal/legacyenv"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fahadjibransheikh/raven/internal/models"
	"github.com/fahadjibransheikh/raven/internal/providers"
)

const (
	pushRenewInterval  = 12 * time.Hour
	pushStartDelay     = 30 * time.Second
	pushRequestTimeout = 30 * time.Second
	// Graph caps Outlook message subscriptions at 4230 minutes; stay under it.
	graphSubscriptionTTL = 4200 * time.Minute
)

// PushConfig enables provider push (Gmail Pub/Sub, Graph change notifications).
// Push is off when Secret is empty.
type PushConfig struct {
	Secret     string
	GmailTopic string
	BaseURL    string
}

func LoadPushConfig() PushConfig {
	return PushConfig{
		Secret:     strings.TrimSpace(legacyenv.Get("RAVEN_PUSH_WEBHOOK_SECRET")),
		GmailTopic: strings.TrimSpace(legacyenv.Get("RAVEN_GMAIL_PUSH_TOPIC")),
		BaseURL:    strings.TrimRight(strings.TrimSpace(legacyenv.Get("RAVEN_BASE_URL")), "/"),
	}
}

// StartPush records cfg and, when push is enabled, keeps provider watches renewed.
// It must be called before the HTTP server starts serving.
func (o *SyncOrchestrator) StartPush(ctx context.Context, cfg PushConfig) {
	o.push = cfg
	if !o.PushEnabled() {
		return
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(pushStartDelay):
		}
		ticker := time.NewTicker(pushRenewInterval)
		defer ticker.Stop()
		for {
			o.renewPushWatches(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (o *SyncOrchestrator) PushEnabled() bool {
	return o != nil && o.push.Secret != ""
}

// PushSecretValid compares candidate to the shared secret in constant time.
func (o *SyncOrchestrator) PushSecretValid(candidate string) bool {
	return o.PushEnabled() && subtle.ConstantTimeCompare([]byte(candidate), []byte(o.push.Secret)) == 1
}

// HandleGmailPush syncs the Gmail accounts named by a Pub/Sub push body. Problems
// are logged, not returned: a non-2xx reply would make Pub/Sub redeliver forever.
func (o *SyncOrchestrator) HandleGmailPush(ctx context.Context, body []byte) {
	var envelope struct {
		Message struct {
			Data string `json:"data"`
		} `json:"message"`
	}
	var note struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		log.Printf("gmail push: decode envelope: %v", err)
		return
	}
	data, err := base64.StdEncoding.DecodeString(envelope.Message.Data)
	if err == nil {
		err = json.Unmarshal(data, &note)
	}
	if err != nil || strings.TrimSpace(note.EmailAddress) == "" {
		log.Printf("gmail push: decode message data: %v", err)
		return
	}
	ids, err := o.db.GetEmailSyncAccountIDsByEmail(ctx, providers.ProviderGmail, note.EmailAddress)
	if err != nil {
		log.Printf("gmail push: find accounts: %v", err)
		return
	}
	for _, id := range ids {
		if o.SyncAccount(context.Background(), id) {
			log.Printf("gmail push account=%s: starting sync", id)
		}
	}
}

// HandleGraphPush syncs one Graph account; lifecycle also renews its subscription.
func (o *SyncOrchestrator) HandleGraphPush(ctx context.Context, accountID string, lifecycle bool) {
	provider, err := o.db.GetAccountProvider(ctx, accountID)
	if err != nil || provider != providers.ProviderOutlook {
		return
	}
	if lifecycle {
		go o.renewPushWatch(context.Background(), accountID)
	}
	if o.SyncAccount(context.Background(), accountID) {
		log.Printf("graph push account=%s: starting sync", accountID)
	}
}

func (o *SyncOrchestrator) renewPushWatches(ctx context.Context) {
	ids, err := o.db.GetAllEmailSyncAccountIDs(ctx)
	if err != nil {
		log.Printf("push watch: list accounts: %v", err)
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		o.renewPushWatch(ctx, id)
	}
}

func (o *SyncOrchestrator) renewPushWatch(ctx context.Context, accountID string) {
	provider, err := o.db.GetAccountProvider(ctx, accountID)
	if err != nil {
		log.Printf("push watch account=%s: %v", accountID, err)
		return
	}
	cfg := &models.AccountConfig{Provider: provider}
	ctx, cancel := context.WithTimeout(ctx, pushRequestTimeout)
	defer cancel()
	switch {
	case o.shouldUseGmailAPIMail(cfg):
		if o.push.GmailTopic == "" {
			return
		}
		err = o.watchGmailAccount(ctx, accountID)
	case o.shouldUseOutlookGraphMail(cfg):
		if o.push.BaseURL == "" {
			return
		}
		err = o.ensureGraphSubscription(ctx, accountID)
	default:
		return
	}
	if err != nil {
		log.Printf("push watch account=%s: %v", accountID, err)
	}
}

// watchGmailAccount (re)starts the 7-day Gmail watch; calling it again is idempotent.
func (o *SyncOrchestrator) watchGmailAccount(ctx context.Context, accountID string) error {
	token, err := o.tokenProvider.GetOAuthTokenForAccount(ctx, accountID)
	if err != nil {
		return err
	}
	body := map[string]any{
		"topicName":           o.push.GmailTopic,
		"labelIds":            []string{"INBOX"},
		"labelFilterBehavior": "include",
	}
	var resp struct {
		Expiration string `json:"expiration"`
	}
	watch := func(token string) error {
		return providerJSON(ctx, http.MethodPost, gmailAPIBaseURL+"/users/me/watch", token, nil, body, &resp)
	}
	err = watch(token)
	if refreshed, ok := o.refreshGmailTokenAfterUnauthorized(ctx, accountID, &token, err); ok {
		err = watch(refreshed)
	}
	if err != nil {
		return err
	}
	expires := resp.Expiration
	if ms, perr := strconv.ParseInt(expires, 10, 64); perr == nil {
		expires = time.UnixMilli(ms).UTC().Format(time.RFC3339)
	}
	log.Printf("gmail push account=%s: watch active until %s", accountID, expires)
	return nil
}

// ensureGraphSubscription extends this account's inbox subscription, creating it
// when missing and deleting duplicates.
// ponytail: reads only the first page of /subscriptions; follow @odata.nextLink if an account ever holds that many.
func (o *SyncOrchestrator) ensureGraphSubscription(ctx context.Context, accountID string) error {
	graphTokens, ok := o.tokenProvider.(graphMailTokenProvider)
	if !ok {
		return nil
	}
	token, err := graphTokens.GetMicrosoftGraphMailTokenForAccount(ctx, accountID)
	if err != nil {
		return err
	}
	notificationURL := o.push.BaseURL + "/hooks/graph?account=" + url.QueryEscape(accountID)
	expires := time.Now().UTC().Add(graphSubscriptionTTL).Format(time.RFC3339)

	var list struct {
		Value []struct {
			ID              string `json:"id"`
			NotificationURL string `json:"notificationUrl"`
		} `json:"value"`
	}
	if err := providerJSON(ctx, http.MethodGet, outlookGraphBaseURL+"/subscriptions", token, nil, nil, &list); err != nil {
		return err
	}
	kept := false
	for _, sub := range list.Value {
		if sub.NotificationURL != notificationURL {
			continue
		}
		endpoint := outlookGraphBaseURL + "/subscriptions/" + url.PathEscape(sub.ID)
		if kept {
			if err := providerJSON(ctx, http.MethodDelete, endpoint, token, nil, nil, nil); err != nil {
				log.Printf("graph push account=%s: delete duplicate subscription: %v", accountID, err)
			}
			continue
		}
		kept = true
		if err := providerJSON(ctx, http.MethodPatch, endpoint, token, nil, map[string]string{"expirationDateTime": expires}, nil); err != nil {
			return err
		}
	}
	if kept {
		log.Printf("graph push account=%s: subscription renewed until %s", accountID, expires)
		return nil
	}
	err = providerJSON(ctx, http.MethodPost, outlookGraphBaseURL+"/subscriptions", token, nil, map[string]string{
		"changeType":               "created",
		"notificationUrl":          notificationURL,
		"lifecycleNotificationUrl": notificationURL,
		"resource":                 "me/mailFolders('inbox')/messages",
		"expirationDateTime":       expires,
		"clientState":              o.push.Secret,
	}, nil)
	if err != nil {
		return err
	}
	log.Printf("graph push account=%s: subscription created until %s", accountID, expires)
	return nil
}
