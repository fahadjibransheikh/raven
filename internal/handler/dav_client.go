package handler

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/auth"
	"github.com/cristianadrielbraun/gofer/internal/netguard"
)

// davPrivateAllowed decides whether a CardDAV connection may reach a private
// address at host:port. Nil means never. New wires it to the deployment mode and
// the admin private-target exception list.
var davPrivateAllowed func(ctx context.Context, host string, port int) bool

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

// davHTTPClient returns a client whose dialer refuses non-public addresses
// (checked on the IP actually connected, so DNS rebinding and redirects cannot
// bypass it) unless the target is the context's anchor and the policy allows it.
func davHTTPClient(ctx context.Context, timeout time.Duration) *http.Client {
	anchor, _ := ctx.Value(davAnchorKey{}).(string)
	dialer := func(ctx context.Context, network, address string) (net.Conn, error) {
		host, portText, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		allowPrivate := false
		if port, perr := strconv.Atoi(portText); perr == nil && anchor != "" && davPrivateAllowed != nil &&
			net.JoinHostPort(strings.ToLower(strings.TrimSuffix(host, ".")), portText) == anchor {
			allowPrivate = davPrivateAllowed(ctx, strings.ToLower(strings.TrimSuffix(host, ".")), port)
		}
		d := &net.Dialer{
			Timeout: 10 * time.Second,
			Control: func(_, address string, _ syscall.RawConn) error {
				if allowPrivate {
					return nil
				}
				h, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				if netguard.ForbiddenIP(net.ParseIP(h)) {
					return fmt.Errorf("refusing to connect to non-public address %s", h)
				}
				return nil
			},
		}
		return d.DialContext(ctx, network, address)
	}
	return &http.Client{
		Timeout: timeout,
		// No proxy: it would be dialled instead of the target, defeating the check.
		Transport: &http.Transport{DialContext: dialer, TLSHandshakeTimeout: 10 * time.Second, MaxResponseHeaderBytes: 64 << 10, DisableKeepAlives: true},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("unsupported redirect scheme %q", req.URL.Scheme)
			}
			return nil
		},
	}
}

// davPrivateTargetAllowed is the deployment policy for typed CardDAV endpoints on
// private addresses. In open and personal mode the single operator owns the
// server and the LAN (a Nextcloud on 192.168.x.x is the common self-host case),
// so their own typed endpoint is allowed. In managed (multi-user) mode any user
// could aim the server at internal services, so only an admin-approved private
// target exception (the same list autodiscovery uses) opens it.
func (h *Handler) davPrivateTargetAllowed(ctx context.Context, host string, port int) bool {
	if h.auth == nil || h.auth.Config().AuthenticationMode() != auth.ModeManaged {
		return true
	}
	for _, protocol := range []string{"https", "http"} {
		if ok, err := h.db.IsPrivateTargetAllowed(ctx, protocol, host, port); err == nil && ok {
			return true
		}
	}
	return false
}
