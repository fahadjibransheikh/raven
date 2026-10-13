// Package netguard keeps Gofer's outgoing connections to servers named by
// users or by message senders off private networks. A user could otherwise
// point a mail account at an internal service, and a sender could point a
// remote image at one, and have the Gofer server connect on their behalf. An
// administrator can approve exact private targets, such as a mail server on
// the local network.
//
// Two entry points share one address policy: Dial/NewHTTPTransport (a resolved
// address is checked, then dialled, with an optional approved-private
// exception) and NewClient/Fetch (a connect-time check on the address the
// socket really opens, with no exception) for URLs chosen by message senders.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// BlockedError reports a target refused because it is on a private network.
type BlockedError struct {
	Protocol string
	Host     string
	Port     int
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("%s server %s is on a private network; it needs a private target exception under Mail security",
		strings.ToUpper(e.Protocol), net.JoinHostPort(e.Host, strconv.Itoa(e.Port)))
}

// IsBlocked reports whether err comes from a refused private target.
func IsBlocked(err error) bool {
	var blocked *BlockedError
	return errors.As(err, &blocked)
}

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("::/96"),           // deprecated IPv4-compatible addresses
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, and broadcast
	netip.MustParsePrefix("100::/64"),        // discard
	netip.MustParsePrefix("2001::/32"),       // Teredo, which hides its IPv4 address
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("fec0::/10"),       // deprecated site-local
}

var (
	nat64Prefixes = []netip.Prefix{netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48")}
	sixToFour     = netip.MustParsePrefix("2002::/16")
)

// IsPrivate reports whether addr is anything but a public internet address,
// including IPv4 addresses carried inside IPv6 forms.
func IsPrivate(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	if addr.Is6() {
		b := addr.As16()
		for _, prefix := range nat64Prefixes {
			if prefix.Contains(addr) {
				return IsPrivate(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
			}
		}
		if sixToFour.Contains(addr) {
			return IsPrivate(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
		}
	}
	return false
}

// Resolver looks up the addresses of a host.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Dialer connects to servers only at checked addresses.
type Dialer struct {
	// Dialer sets timeouts and keep-alives. It is never given a host name.
	Dialer   *net.Dialer
	Resolver Resolver
	// Strict also refuses every NAT64 and 6to4 address, whatever IPv4 address
	// it embeds. HTTP fetches of URLs chosen by message senders and servers
	// set it; IMAP and SMTP leave it off so IPv6-only networks with DNS64
	// still reach public mail servers.
	Strict bool
}

// isTunnel reports whether addr is in a prefix that carries an IPv4 address
// inside an IPv6 one (NAT64, 6to4).
func isTunnel(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.Is6() {
		return false
	}
	for _, prefix := range nat64Prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return sixToFour.Contains(addr)
}

// ForbiddenIP reports whether ip is not a public unicast address, counting
// every NAT64 and 6to4 address as forbidden.
func ForbiddenIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	return forbiddenAddr(addr)
}

func forbiddenAddr(addr netip.Addr) bool {
	addr = addr.WithZone("")
	return IsPrivate(addr) || isTunnel(addr)
}

// Dial connects to host:port for protocol. Private addresses are refused
// unless allowPrivate is set. It connects to the address it checked, so a DNS
// answer can't change between the check and the connection.
func (d *Dialer) Dial(ctx context.Context, protocol, host string, port int, allowPrivate bool) (net.Conn, error) {
	addrs, err := d.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	if !allowPrivate {
		// One private answer refuses the host, so a name that resolves to
		// both a public and a private address can't be used to reach either.
		for _, addr := range addrs {
			if IsPrivate(addr) || (d.Strict && isTunnel(addr)) {
				return nil, &BlockedError{Protocol: protocol, Host: host, Port: port}
			}
		}
	}
	dialer := d.Dialer
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	var lastErr error
	for _, addr := range addrs {
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(addr.Unmap().String(), strconv.Itoa(port)))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (d *Dialer) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	host = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(host), "["), "]")
	if host == "" {
		return nil, errors.New("server host is required")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			return nil, fmt.Errorf("server address %q must not name a network interface", host)
		}
		return []netip.Addr{addr}, nil
	}
	resolver := d.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("server %q has no addresses", host)
	}
	return addrs, nil
}

// IsPrivateHost reports whether host, or any address it resolves to, is
// private, so Dial would refuse it without an exception. It only looks up the
// name and never connects.
func (d *Dialer) IsPrivateHost(ctx context.Context, host string) (bool, error) {
	addrs, err := d.resolve(ctx, host)
	if err != nil {
		return false, err
	}
	for _, addr := range addrs {
		if IsPrivate(addr) || (d.Strict && isTunnel(addr)) {
			return true, nil
		}
	}
	return false, nil
}

// IsPrivateHost checks host with the default resolver.
func IsPrivateHost(ctx context.Context, host string) (bool, error) {
	return (&Dialer{}).IsPrivateHost(ctx, host)
}

// Dial connects with a default Dialer.
func Dial(ctx context.Context, dialer *net.Dialer, protocol, host string, port int, allowPrivate bool) (net.Conn, error) {
	return (&Dialer{Dialer: dialer}).Dial(ctx, protocol, host, port, allowPrivate)
}

const maxRedirects = 5

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
		CheckRedirect: CheckRedirect,
	}
}

// CheckRedirect is the redirect policy of guarded clients: at most five
// redirects, http(s) only. Clients built on NewHTTPTransport set it too, since
// the transport checks addresses but not the redirect count or scheme.
func CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirects {
		return fmt.Errorf("netguard: stopped after %d redirects", maxRedirects)
	}
	return checkScheme(req)
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
	return FetchContext(context.Background(), rawURL, maxBytes)
}

// FetchContext is Fetch with a caller-controlled context.
func FetchContext(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	return fetchContext(ctx, NewClient(10*time.Second), rawURL, maxBytes)
}

func fetch(client *http.Client, rawURL string, maxBytes int64) ([]byte, error) {
	return fetchContext(context.Background(), client, rawURL, maxBytes)
}

func fetchContext(ctx context.Context, client *http.Client, rawURL string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
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
