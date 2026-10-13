package handler

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/fahadjibransheikh/raven/internal/netguard"
)

// davHTTPTransport carries every CalDAV request. People enter those servers
// themselves, so connections go through netguard (checked as they connect, so
// DNS rebinding and redirects cannot bypass it). It reuses no connection, so a
// private address approved for one request is never reached over a connection
// kept for another. Tests replace the transport to reach their local servers.
var davHTTPTransport http.RoundTripper = netguard.NewHTTPTransportNoReuse(davPrivateTargetAllowed)

var davPrivateTargetPolicy atomic.Pointer[netguard.PrivatePolicy]

// davOperatorOwnsNetwork reports whether this instance has a single operator
// who owns the server and its LAN. Nil (no handler built) means no.
var davOperatorOwnsNetwork atomic.Pointer[func() bool]

// setDAVPrivateTargetPolicy points DAV connections at the instance's mail
// security exceptions. The transport is shared by every request, so the
// policy is set once, when the handler is built.
func setDAVPrivateTargetPolicy(policy netguard.PrivatePolicy) {
	davPrivateTargetPolicy.Store(&policy)
}

// setDAVOperatorOwnsNetwork sets how DAV connections learn the authentication
// mode, once, when the handler is built.
func setDAVOperatorOwnsNetwork(owns func() bool) {
	davOperatorOwnsNetwork.Store(&owns)
}

// davPrivatePolicy is the one rule for CalDAV and CardDAV connections to a
// private address. An administrator's private-target exception approves it in
// every mode. Otherwise only the anchor (the exact host:port the user typed or
// saved) may be private, and only where a single operator owns the server and
// its LAN: open and personal mode. In managed mode any user could aim the
// server at internal services, so the anchor alone is not enough. Hosts learned
// from server responses are never the anchor.
func davPrivatePolicy(ctx context.Context, protocol, host string, port int, isAnchor bool) bool {
	if policy := davPrivateTargetPolicy.Load(); policy != nil && *policy != nil && (*policy)(ctx, protocol, host, port) {
		return true
	}
	owns := davOperatorOwnsNetwork.Load()
	return isAnchor && owns != nil && *owns != nil && (*owns)()
}

// davPrivateTargetAllowed is the CalDAV transport's policy: the anchor is the
// one carried by the request context (withDAVAnchor).
func davPrivateTargetAllowed(ctx context.Context, protocol, host string, port int) bool {
	anchor, _ := ctx.Value(davAnchorKey{}).(string)
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return davPrivatePolicy(ctx, protocol, host, port, anchor != "" && net.JoinHostPort(host, strconv.Itoa(port)) == anchor)
}

// withCalDAVAnchor anchors ctx at a CalDAV server URL the user typed or saved,
// in any form people enter it. An empty or unusable URL leaves ctx unchanged.
func withCalDAVAnchor(ctx context.Context, rawBaseURL string) context.Context {
	base, err := normalizeCalDAVBaseURL(rawBaseURL)
	if err != nil {
		return ctx
	}
	return withDAVAnchor(ctx, base)
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
