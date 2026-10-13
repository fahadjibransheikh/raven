package imap

import (
	"net"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/models"
	"github.com/fahadjibransheikh/raven/internal/netguard"
)

// A user could point an account at an internal service. Without an
// administrator's exception, Raven must not even open the connection.
func TestConnectionRefusesUnapprovedPrivateServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		if conn, err := listener.Accept(); err == nil {
			accepted <- struct{}{}
			_ = conn.Close()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	for _, mode := range []string{"tls", "starttls", "plaintext"} {
		err := TestConnection(t.Context(), &models.AccountConfig{IMAPHost: "127.0.0.1", IMAPPort: port, IMAPTLSMode: mode, IMAPAllowPlaintext: true, AuthMethod: "plain", Username: "user"}, "secret")
		if !netguard.IsBlocked(err) {
			t.Fatalf("%s: TestConnection() error = %v, want a private target refusal", mode, err)
		}
	}
	select {
	case <-accepted:
		t.Fatal("the private server was contacted")
	case <-time.After(50 * time.Millisecond):
	}
}
