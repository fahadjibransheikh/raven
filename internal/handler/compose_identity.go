package handler

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/cristianadrielbraun/gofer/internal/models"
	"github.com/cristianadrielbraun/gofer/internal/storage"
)

// resolveComposeIdentity returns the From name and address for a compose
// request. fromEmail must be an identity of this account (looked up by owner,
// so nobody can send as an arbitrary address); empty means the account's
// default identity. The name is the identity's, else the account display name:
// the sidebar Label never reaches outgoing mail.
func (h *Handler) resolveComposeIdentity(ctx context.Context, account *models.Account, fromEmail string) (name, email string, cerr *composeRequestError) {
	userID := h.userID(ctx)
	fromEmail = strings.TrimSpace(fromEmail)
	var identity models.AccountIdentity
	if fromEmail == "" {
		identities, err := h.db.ListAccountIdentities(ctx, userID, account.ID)
		if err != nil {
			log.Printf("compose identities account=%s: %v", account.ID, err)
			return "", "", &composeRequestError{status: http.StatusInternalServerError, message: "failed to load sending addresses"}
		}
		found := false
		for _, candidate := range identities {
			if candidate.IsDefault {
				identity, found = candidate, true
				break
			}
		}
		if !found { // no identity rows: behave exactly as before B1
			return account.Name, account.Email, nil
		}
	} else {
		var err error
		identity, err = h.db.IdentityForAccount(ctx, userID, account.ID, fromEmail)
		if errors.Is(err, storage.ErrIdentityNotFound) {
			return "", "", &composeRequestError{status: http.StatusBadRequest, message: "That sending address does not belong to this account."}
		}
		if err != nil {
			log.Printf("compose identity account=%s: %v", account.ID, err)
			return "", "", &composeRequestError{status: http.StatusInternalServerError, message: "failed to load sending address"}
		}
	}
	name = strings.TrimSpace(identity.Name)
	if name == "" {
		name = account.Name
	}
	email = identity.Email
	if identity.Source == models.IdentitySourcePrimary {
		email = account.Email // keep the account's own casing, as before
	}
	return name, email, nil
}

// pickReplyIdentity chooses which identity a reply/forward should be sent
// from. For a message in Sent (we wrote it) it is the identity matching the
// source's From; otherwise the identity addressed in To, then Cc, then one named
// in Delivered-To / X-Original-To (comma-joined, as stored; this catches Bcc'd list
// mail and mail forwarded to an alias). Falls back to the account's default
// identity, or "" if the account has none.
func pickReplyIdentity(identities []models.AccountIdentity, folderRole string, from models.Contact, to, cc []models.Contact, deliveredTo string) string {
	byEmail := make(map[string]string, len(identities))
	fallback := ""
	for _, id := range identities {
		byEmail[strings.ToLower(strings.TrimSpace(id.Email))] = id.Email
		if id.IsDefault {
			fallback = id.Email
		}
	}
	match := func(contacts ...models.Contact) string {
		for _, c := range contacts {
			if email, ok := byEmail[strings.ToLower(strings.TrimSpace(c.Email))]; ok {
				return email
			}
		}
		return ""
	}
	if folderRole == "sent" {
		if email := match(from); email != "" {
			return email
		}
		return fallback
	}
	if email := match(to...); email != "" {
		return email
	}
	if email := match(cc...); email != "" {
		return email
	}
	var delivered []models.Contact
	for _, a := range strings.Split(deliveredTo, ",") {
		delivered = append(delivered, models.Contact{Email: a})
	}
	if email := match(delivered...); email != "" {
		return email
	}
	return fallback
}
