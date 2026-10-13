package notifications

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Browsers name their push service when they subscribe, so delivery never
// reaches a private address.
func TestPushDeliveryRefusesPrivateAddresses(t *testing.T) {
	contacted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { contacted = true }))
	defer server.Close()
	_, err := pushHTTPClient.Post(server.URL+"/push", "application/octet-stream", strings.NewReader("x"))
	if err == nil || !strings.Contains(err.Error(), "private network") || contacted {
		t.Fatalf("push to a private address: err=%v contacted=%v", err, contacted)
	}
}
