package views

import (
	"context"
	"net/http"
)

type mailboxOAuthKey struct{}

type mailboxOAuth struct{ google, microsoft bool }

// WithMailboxOAuth records which "sign in with" providers this Raven has an
// OAuth client for, so the add-account chooser only offers those. Without it
// (tests, other renderers) both are offered.
func WithMailboxOAuth(google, microsoft bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), mailboxOAuthKey{}, mailboxOAuth{google, microsoft})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func mailboxOAuthFrom(ctx context.Context) mailboxOAuth {
	if v, ok := ctx.Value(mailboxOAuthKey{}).(mailboxOAuth); ok {
		return v
	}
	return mailboxOAuth{google: true, microsoft: true}
}
