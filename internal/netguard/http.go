package netguard

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// PrivatePolicy approves one private target, such as a calendar server on the
// local network, for protocol ("http" or "https"), host and port.
type PrivatePolicy func(ctx context.Context, protocol, host string, port int) bool

// NewHTTPTransport returns an HTTP transport whose every connection goes
// through Dial: private addresses only when allowPrivate approves them, no
// proxy from the environment, and each redirect checked again as it connects.
// A nil policy approves no private target.
func NewHTTPTransport(allowPrivate PrivatePolicy) http.RoundTripper {
	return guardedTransport{inner: newGuardedHTTPTransport(allowPrivate)}
}

// NewHTTPTransportNoReuse is NewHTTPTransport without connection reuse, so
// every request is checked by the policy as it connects, and a private target
// whose approval was withdrawn is not reached over an earlier connection.
func NewHTTPTransportNoReuse(allowPrivate PrivatePolicy) http.RoundTripper {
	inner := newGuardedHTTPTransport(allowPrivate)
	inner.DisableKeepAlives = true
	return guardedTransport{inner: inner}
}

type guardedTransport struct{ inner *http.Transport }

func (t guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if override := testTransport.Load(); override != nil {
		return (*override).RoundTrip(req)
	}
	return t.inner.RoundTrip(req)
}

var testTransport atomic.Pointer[http.RoundTripper]

// UseTransportForTests sends every guarded transport's requests through rt
// until the returned function restores the previous one. It is for tests in
// other packages that serve outbound requests from local servers; production
// code never calls it.
func UseTransportForTests(rt http.RoundTripper) (restore func()) {
	previous := testTransport.Swap(&rt)
	return func() { testTransport.Store(previous) }
}

func newGuardedHTTPTransport(allowPrivate PrivatePolicy) *http.Transport {
	dialer := &Dialer{Dialer: &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}, Strict: true}
	dial := func(ctx context.Context, protocol, address string) (net.Conn, error) {
		host, portText, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			return nil, err
		}
		allowed := allowPrivate != nil && allowPrivate(ctx, protocol, host, port)
		return dialer.Dial(ctx, protocol, host, port, allowed)
	}
	return &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return dial(ctx, "http", address)
		},
		DialTLSContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			conn, err := dial(ctx, "https", address)
			if err != nil {
				return nil, err
			}
			host, _, _ := net.SplitHostPort(address)
			tlsConn := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return tlsConn, nil
		},
		MaxIdleConns:           32,
		IdleConnTimeout:        90 * time.Second,
		ResponseHeaderTimeout:  30 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
}
