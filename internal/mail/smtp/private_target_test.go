package smtp

import (
	"net"
	"testing"
	"time"

	"github.com/fahadjibransheikh/raven/internal/models"
	"github.com/fahadjibransheikh/raven/internal/netguard"
)

func TestNewClientRefusesUnapprovedPrivateServer(t *testing.T) {
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
		_, err := NewClient(t.Context(), &models.AccountConfig{SMTPHost: "127.0.0.1", SMTPPort: port, SMTPTLSMode: mode, SMTPAllowPlaintext: true, AuthMethod: "plain", Username: "user"}, "secret")
		if !netguard.IsBlocked(err) {
			t.Fatalf("%s: NewClient() error = %v, want a private target refusal", mode, err)
		}
		// Retrying won't help until an administrator approves the server.
		if IsRetryable(err) {
			t.Fatalf("%s: refusal is marked retryable", mode)
		}
	}
	select {
	case <-accepted:
		t.Fatal("the private server was contacted")
	case <-time.After(50 * time.Millisecond):
	}
}
