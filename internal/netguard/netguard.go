// Package netguard makes outbound HTTP requests to attacker-influenced URLs
// (remote email content, web-push endpoints) safe from SSRF. The check runs on
// the address the socket actually connects to, so DNS rebinding and redirects
// cannot reach internal services.
package netguard

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

const maxRedirects = 5

var forbiddenPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{
		// IPv4: "this" network, private, CGNAT (incl. 100.100.100.200 metadata), loopback, link-local
		// (169.254.169.254 metadata), IETF/benchmark/documentation ranges, multicast, reserved+broadcast.
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
		"224.0.0.0/4", "240.0.0.0/4",
		// IPv6: unspecified/loopback/IPv4-compatible, ULA, link-local, multicast, discard, documentation,
		// and the tunnelling prefixes that embed an IPv4 address (NAT64, Teredo, 6to4).
		"::/96", "fc00::/7", "fe80::/10", "ff00::/8", "100::/64", "2001::/32", "2001:db8::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48",
	} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

// ForbiddenIP reports whether ip is not a public unicast address.
func ForbiddenIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	return forbiddenAddr(addr)
}

func forbiddenAddr(addr netip.Addr) bool {
	addr = addr.Unmap().WithZone("")
	if !addr.IsValid() || addr.IsLoopback() || addr.IsUnspecified() || addr.IsMulticast() || addr.IsPrivate() || addr.IsLinkLocalUnicast() {
		return true
	}
	for _, p := range forbiddenPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// NewClient returns an HTTP client that only connects to public addresses,
// follows at most five redirects (http/https only) and ignores proxy settings
// (a proxy would be dialled instead of the target, defeating the check).
func NewClient(timeout time.Duration) *http.Client {
	return newClient(timeout, func(ap netip.AddrPort) bool { return forbiddenAddr(ap.Addr()) })
}

func newClient(timeout time.Duration, blocked func(netip.AddrPort) bool) *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return err
			}
			if blocked(ap) {
				return fmt.Errorf("netguard: refusing to connect to %s", ap.Addr())
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            dialer.DialContext,
			TLSHandshakeTimeout:    10 * time.Second,
			MaxResponseHeaderBytes: 64 << 10,
			DisableKeepAlives:      true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("netguard: stopped after %d redirects", maxRedirects)
			}
			return checkScheme(req)
		},
	}
}

func checkScheme(req *http.Request) error {
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return fmt.Errorf("netguard: unsupported scheme %q", req.URL.Scheme)
	}
	if req.URL.Host == "" {
		return errors.New("netguard: URL has no host")
	}
	return nil
}

// Fetch GETs an http(s) URL through a guarded client and returns the body. A
// non-200 status or a body over maxBytes is an error (never a silent truncation).
func Fetch(rawURL string, maxBytes int64) ([]byte, error) {
	return fetch(NewClient(10*time.Second), rawURL, maxBytes)
}

func fetch(client *http.Client, rawURL string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if err := checkScheme(req); err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("netguard: response too large (over %d bytes)", maxBytes)
	}
	return data, nil
}

// ValidEndpoint reports whether raw is a usable push endpoint: https, a host,
// no credentials, and not an IP literal in a forbidden range. Hostnames are
// checked again at connect time by the guarded client.
func ValidEndpoint(raw string) bool {
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil || req.URL.Scheme != "https" || req.URL.Host == "" || req.URL.User != nil {
		return false
	}
	if addr, err := netip.ParseAddr(strings.Trim(req.URL.Hostname(), "[]")); err == nil && forbiddenAddr(addr) {
		return false
	}
	return true
}
