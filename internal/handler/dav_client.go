package handler

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fahadjibransheikh/raven/internal/netguard"
)

// davPrivateAllowed decides whether a CardDAV connection to the anchor may
// reach a private address at host:port (the transport has already checked it is
// the anchor). Nil means never. By default davPrivatePolicy decides: an
// administrator's private-target exception in every mode, or the typed
// endpoint itself where one operator owns the network. Tests replace it.
var davPrivateAllowed = func(ctx context.Context, host string, port int) bool {
	return davPrivatePolicy(ctx, "https", host, port, true) || davPrivatePolicy(ctx, "http", host, port, true)
}

type davAnchorKey struct{}

// withDAVAnchor marks rawURL's host:port as the endpoint the user typed (or the
// account's saved address book). Only that exact host:port can ever be exempt
// from the private-address block; hosts learned from server responses
// (principal/home hrefs, redirects, SRV records) are always held to the strict
// public-only rule.
func withDAVAnchor(ctx context.Context, rawURL string) context.Context {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Hostname() == "" {
		return ctx
	}
	return context.WithValue(ctx, davAnchorKey{}, davHostPort(u))
}

func davHostPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")), port)
}

// davHTTPClient returns a client for CardDAV requests. Its connections go
// through netguard, which checks the address actually connected (so DNS
// rebinding and redirects cannot bypass it), and refuses private addresses
// unless the target is the context's anchor and davPrivateAllowed approves it.
func davHTTPClient(ctx context.Context, timeout time.Duration) *http.Client {
	anchor, _ := ctx.Value(davAnchorKey{}).(string)
	return &http.Client{Timeout: timeout, Transport: davAnchoredTransport(anchor), CheckRedirect: netguard.CheckRedirect}
}

// davAnchoredTransports holds one guarded transport per anchor, so requests
// to the same server reuse connections. Anchors are typed endpoints, a handful.
var davAnchoredTransports sync.Map

func davAnchoredTransport(anchor string) http.RoundTripper {
	if cached, ok := davAnchoredTransports.Load(anchor); ok {
		return cached.(http.RoundTripper)
	}
	transport := netguard.NewHTTPTransportNoReuse(func(ctx context.Context, _, host string, port int) bool {
		if anchor == "" || davPrivateAllowed == nil {
			return false
		}
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		return net.JoinHostPort(host, strconv.Itoa(port)) == anchor && davPrivateAllowed(ctx, host, port)
	})
	actual, _ := davAnchoredTransports.LoadOrStore(anchor, transport)
	return actual.(http.RoundTripper)
}
