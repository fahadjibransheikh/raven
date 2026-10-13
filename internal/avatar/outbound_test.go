package avatar

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Avatar addresses come from senders' domains, so the resolver never
// connects to a private address, whatever a sender's DNS says.
func TestResolverClientRefusesPrivateAddresses(t *testing.T) {
	contacted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { contacted = true }))
	defer server.Close()
	_, err := NewResolver().client.Get(server.URL + "/favicon.ico")
	if err == nil || !strings.Contains(err.Error(), "private network") || contacted {
		t.Fatalf("avatar fetch from a private address: err=%v contacted=%v", err, contacted)
	}
}
