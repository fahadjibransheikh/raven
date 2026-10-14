package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	avatarresolver "github.com/fahadjibransheikh/raven/internal/avatar"
	"github.com/fahadjibransheikh/raven/internal/netguard"
	"golang.org/x/oauth2"
)

const (
	// The source of an avatar taken from the Google profile of a connected
	// Gmail mailbox, and the provider name its lookups are logged under.
	avatarGoogleProfile = "google_profile"
	// A Google lookup that failed is tried again after this long. Until then
	// the avatar it was meant to replace, or the photo it failed to renew,
	// stays as it is.
	avatarGoogleProfileRetryAfter = avatarErrorRetryAfter
)

// googleProfilePhotoSize matches the size suffix userinfo puts on the photo
// address (=s96-c).
var googleProfilePhotoSize = regexp.MustCompile(`=s\d+(-c)?$`)

// ownGmailAccountID returns the connected Gmail mailbox that email is the
// address of, or "" when it is not one or Gmail sign-in is not set up. Only
// such an address is looked up at Google; every other sender goes through the
// ordinary providers.
func (h *Handler) ownGmailAccountID(ctx context.Context, email string) (string, error) {
	if h.mailCredentials() == nil {
		return "", nil
	}
	return h.db.GetGmailAccountIDByEmail(ctx, email)
}

// fetchGoogleProfileAvatar tries the Google profile photo of one of the user's
// own Gmail mailboxes. It reports true when the avatar was saved, or when an
// earlier photo was kept because Google could not be reached; false sends the
// address on to the ordinary providers. The outcome's status is empty when the
// lookup did not finish.
func (h *Handler) fetchGoogleProfileAvatar(ctx context.Context, hash, email, accountID string, validate func() error) (avatarresolver.Image, bool, avatarProviderOutcome, error) {
	outcome := avatarProviderOutcome{provider: avatarGoogleProfile}
	if err := validate(); err != nil {
		return avatarresolver.Image{}, false, outcome, err
	}
	image, found, err := h.googleProfilePhoto(ctx, accountID)
	if guardErr := validate(); guardErr != nil {
		return avatarresolver.Image{}, false, outcome, guardErr
	}
	switch {
	case err != nil:
		outcome.status = "error"
		_ = h.db.RecordSenderAvatarAttempt(ctx, hash, email, avatarGoogleProfile, "error", googleProfileErrorAttemptMessage(err))
		// A photo already saved outlives a failed renewal; handing the address
		// to the site-icon providers would replace it with the company logo.
		kept, keepErr := h.db.ExtendSenderAvatar(ctx, hash, avatarGoogleProfile, time.Now().Add(avatarGoogleProfileRetryAfter))
		return avatarresolver.Image{Source: avatarGoogleProfile}, kept, outcome, keepErr
	case !found:
		outcome.status = "missing"
		_ = h.db.RecordSenderAvatarAttempt(ctx, hash, email, avatarGoogleProfile, "missing", googleProfileMissingAttemptMessage())
		return avatarresolver.Image{}, false, outcome, nil
	}
	outcome.status = "found"
	_ = h.db.RecordSenderAvatarAttempt(ctx, hash, email, avatarGoogleProfile, "found", avatarFoundAttemptMessage(image))
	image, found, err = h.persistFoundAvatar(ctx, hash, email, image, "unchecked", "unchecked")
	return image, found, outcome, err
}

// googleProfilePhoto downloads the Google profile photo of a connected Gmail
// mailbox. The token comes from the same store the mailbox sync uses.
func (h *Handler) googleProfilePhoto(ctx context.Context, accountID string) (avatarresolver.Image, bool, error) {
	credentials := h.mailCredentials()
	token, err := credentials.GetOAuthTokenForAccount(ctx, accountID)
	if err != nil {
		return avatarresolver.Image{}, false, err
	}
	info, err := credentials.GetGoogleAccountInfo(ctx, &oauth2.Token{AccessToken: token, TokenType: "Bearer"})
	if err != nil {
		return avatarresolver.Image{}, false, err
	}
	rawURL := strings.TrimSpace(info.Picture)
	if rawURL == "" {
		return avatarresolver.Image{}, false, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || !isAllowedProviderAvatarHost(parsed.Hostname()) {
		return avatarresolver.Image{}, false, errors.New("userinfo returned a photo address outside Google's image hosts")
	}
	photoURL := googleProfilePhotoSize.ReplaceAllString(rawURL, "=s256-c")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, photoURL, nil)
	if err != nil {
		return avatarresolver.Image{}, false, err
	}
	req.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/gif,*/*;q=0.8")
	client := h.providerAvatarHTTPClient
	if client == nil {
		client = netguard.NewClient(15 * time.Second)
	}
	resp, err := client.Do(req)
	if err != nil {
		return avatarresolver.Image{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return avatarresolver.Image{}, false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return avatarresolver.Image{}, false, fmt.Errorf("GET %s -> %d", photoURL, resp.StatusCode)
	}
	contentType, ok := avatarresolver.ImageContentType(resp.Header.Get("Content-Type"))
	if !ok {
		return avatarresolver.Image{}, false, fmt.Errorf("GET %s returned non-image content type %q", photoURL, resp.Header.Get("Content-Type"))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, providerAvatarMaxBytes+1))
	if err != nil {
		return avatarresolver.Image{}, false, err
	}
	if len(data) > providerAvatarMaxBytes {
		return avatarresolver.Image{}, false, fmt.Errorf("GET %s image exceeds %d bytes", photoURL, providerAvatarMaxBytes)
	}
	if len(data) == 0 {
		return avatarresolver.Image{}, false, nil
	}
	return avatarresolver.Image{Data: data, ContentType: contentType, Source: avatarGoogleProfile, SourceURL: photoURL}, true, nil
}

func googleProfileMissingAttemptMessage() string {
	return "Google userinfo -> no profile photo for this mailbox"
}

func googleProfileErrorAttemptMessage(err error) string {
	return fmt.Sprintf("Google profile photo lookup failed: %v", err)
}
