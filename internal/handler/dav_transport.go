package handler

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/cristianadrielbraun/gofer/internal/netguard"
)

// davHTTPTransport carries every CalDAV and CardDAV request. People enter
// those servers themselves, so connections go through netguard and reach a
// private address only when the mail security exceptions approve it. Tests
// replace the transport to reach their local servers.
var davHTTPTransport http.RoundTripper = netguard.NewHTTPTransport(davPrivateTargetAllowed)

var davPrivateTargetPolicy atomic.Pointer[netguard.PrivatePolicy]

// setDAVPrivateTargetPolicy points DAV connections at the instance's mail
// security exceptions. The transport is shared by every request, so the
// policy is set once, when the handler is built.
func setDAVPrivateTargetPolicy(policy netguard.PrivatePolicy) {
	davPrivateTargetPolicy.Store(&policy)
}

func davPrivateTargetAllowed(ctx context.Context, protocol, host string, port int) bool {
	policy := davPrivateTargetPolicy.Load()
	return policy != nil && *policy != nil && (*policy)(ctx, protocol, host, port)
}

// validPushEndpoint accepts the HTTPS address of a browser's push service.
// The browser supplies it, so it is delivered to only through netguard as
// well; this refuses anything that isn't a plain HTTPS URL to begin with.
func validPushEndpoint(endpoint string) bool {
	if len(endpoint) > 2048 {
		return false
	}
	u, err := url.Parse(endpoint)
	return err == nil && strings.EqualFold(u.Scheme, "https") && u.Host != "" && u.User == nil
}
