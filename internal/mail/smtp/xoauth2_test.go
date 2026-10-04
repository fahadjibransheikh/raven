package smtp

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

// fakeSTARTTLSServer speaks just enough SMTP to accept XOAUTH2 over STARTTLS.
// finalReply answers the end of DATA.
type fakeSTARTTLSServer struct {
	addr      string
	authLine  chan string
	mailFrom  chan string
	finalErr  chan error
	finalLine string
}

func startFakeSTARTTLSServer(t *testing.T, finalReply string) *fakeSTARTTLSServer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	testRootCAs = pool
	t.Cleanup(func() { testRootCAs = nil })

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	srv := &fakeSTARTTLSServer{addr: listener.Addr().String(), authLine: make(chan string, 1), mailFrom: make(chan string, 1), finalErr: make(chan error, 1)}
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		r := bufio.NewReader(conn)
		line := func() string { l, _ := r.ReadString('\n'); return strings.TrimRight(l, "\r\n") }
		fmt.Fprint(conn, "220 fake ESMTP\r\n")
		line() // EHLO
		fmt.Fprint(conn, "250-fake\r\n250 STARTTLS\r\n")
		if line() != "STARTTLS" {
			return
		}
		fmt.Fprint(conn, "220 go ahead\r\n")
		tconn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}})
		if err := tconn.Handshake(); err != nil {
			return
		}
		r = bufio.NewReader(tconn)
		w := func(s string) { _, _ = tconn.Write([]byte(s)) }
		line() // EHLO
		w("250-fake\r\n250-AUTH XOAUTH2\r\n250 SIZE 1000000\r\n")
		srv.authLine <- line()
		w("235 2.7.0 Authentication successful\r\n")
		srv.mailFrom <- line()
		w("250 OK\r\n")
		line() // RCPT
		w("250 OK\r\n")
		line() // DATA
		w("354 go\r\n")
		for line() != "." {
		}
		w(finalReply)
		line() // QUIT or EOF
	}()
	return srv
}

func outlookSMTPTestConfig(srv *fakeSTARTTLSServer) *models.AccountConfig {
	host, port, _ := net.SplitHostPort(srv.addr)
	var p int
	fmt.Sscanf(port, "%d", &p)
	return &models.AccountConfig{SMTPHost: host, SMTPPort: p, SMTPTLSMode: "starttls", AuthMethod: "oauth2", SmtpUsername: "person@msn.com"}
}

func TestOAuth2SMTPAuthenticatesWithXOAUTH2OverSTARTTLS(t *testing.T) {
	srv := startFakeSTARTTLSServer(t, "250 2.0.0 OK queued\r\n")
	result, err, _ := SendRawMessageWithTiming(context.Background(), outlookSMTPTestConfig(srv), "access-token-123", "person@msn.com", []string{"to@example.com"}, []byte("From: Alias <alias@outlook.com>\r\n\r\nhi\r\n"))
	if err != nil || result != models.SendSuccess {
		t.Fatalf("send = %v, %v", result, err)
	}
	auth := <-srv.authLine
	fields := strings.Fields(auth)
	if len(fields) != 3 || fields[0] != "AUTH" || fields[1] != "XOAUTH2" {
		t.Fatalf("auth line = %q, want AUTH XOAUTH2 <initial response>", auth)
	}
	decoded, err := base64.StdEncoding.DecodeString(fields[2])
	if err != nil {
		t.Fatal(err)
	}
	if want := "user=person@msn.com\x01auth=Bearer access-token-123\x01\x01"; string(decoded) != want {
		t.Fatalf("XOAUTH2 = %q, want %q", decoded, want)
	}
	if from := <-srv.mailFrom; !strings.HasPrefix(from, "MAIL FROM:<person@msn.com>") {
		t.Fatalf("envelope = %q, want the account address", from)
	}
}

func TestOAuth2SMTPSendAsDeniedIsAPermanentFailure(t *testing.T) {
	srv := startFakeSTARTTLSServer(t, "550 5.7.60 SMTP; Client does not have permissions to send as this sender\r\n")
	result, err, _ := SendRawMessageWithTiming(context.Background(), outlookSMTPTestConfig(srv), "tok", "person@msn.com", []string{"to@example.com"}, []byte("From: alias@outlook.com\r\n\r\nhi\r\n"))
	if result != models.SendFailed || err == nil || !strings.Contains(err.Error(), "permissions to send as") {
		t.Fatalf("send = %v, %v; want a failure naming the send-as denial", result, err)
	}
	if IsRetryable(err) {
		t.Fatalf("5.7.60 must not be retried: %v", err)
	}
}
