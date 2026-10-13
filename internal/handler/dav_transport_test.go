package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/auth"
	"github.com/fahadjibransheikh/raven/internal/models"
)

// In managed mode people enter CalDAV and CardDAV servers themselves, so a
// server on the Raven host's network is refused unless a mail security
// exception approves it. (Open and personal mode: TestDAVPrivateAnchorByMode.)
func TestDAVRequestsRefuseUnapprovedPrivateServers(t *testing.T) {
	useDAVMode(t, auth.ModeManaged, nil)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:"/>`))
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	previous := davPrivateTargetPolicy.Load()
	t.Cleanup(func() { davPrivateTargetPolicy.Store(previous) })
	cfg := models.ContactSyncConfig{BaseURL: server.URL + "/", AddressBookURL: server.URL + "/books/default/", Username: "lena@example.test"}

	setDAVPrivateTargetPolicy(nil)
	err := testCardDAVAddressBook(t.Context(), cfg, "secret")
	if err == nil || !strings.Contains(err.Error(), "private network") || hits.Load() != 0 {
		t.Fatalf("unapproved private DAV server: err=%v hits=%d", err, hits.Load())
	}

	setDAVPrivateTargetPolicy(func(_ context.Context, protocol, host string, p int) bool {
		return protocol == "http" && host == "127.0.0.1" && p == port
	})
	_ = testCardDAVAddressBook(t.Context(), cfg, "secret")
	if hits.Load() == 0 {
		t.Fatal("an approved private DAV server was not contacted")
	}
}

func TestPushSubscriptionsNeedAnHTTPSPushService(t *testing.T) {
	for endpoint, want := range map[string]bool{
		"https://push.example.test/send/abc":   true,
		"http://push.example.test/send/abc":    false,
		"https://user:pass@push.example.test":  false,
		"ftp://push.example.test/":             false,
		"not a url":                            false,
		"https://" + strings.Repeat("a", 2100): false,
	} {
		if got := validPushEndpoint(endpoint); got != want {
			t.Errorf("validPushEndpoint(%.40q) = %v, want %v", endpoint, got, want)
		}
	}
}
