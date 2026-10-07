package handler

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// The default remote-content downloader must not reach internal addresses.
func TestDownloadRemoteResourceRefusesInternalTargets(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = w.Write([]byte("secret"))
	}))
	defer srv.Close()
	if data, err := downloadRemoteResource(srv.URL + "/latest/meta-data/"); err == nil || hit || len(data) > 0 {
		t.Fatalf("downloadRemoteResource(loopback) = %q, %v (hit=%v), want refusal", data, err, hit)
	}
}

func TestSavePushSubscriptionRejectsUnsafeEndpoints(t *testing.T) {
	h, _ := newAccountOwnershipTestHandler(t)
	post := func(endpoint string) int {
		body := `{"endpoint":"` + endpoint + `","keys":{"p256dh":"k","auth":"a"}}`
		req := httptest.NewRequest(http.MethodPost, "/api/push/subscribe", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.handleSavePushSubscription(rec, ownerRequest(req))
		return rec.Code
	}
	for _, bad := range []string{
		"http://fcm.googleapis.com/send/x", "https://127.0.0.1/x", "https://[::1]/x", "https://169.254.169.254/latest",
		"https://user:pw@fcm.googleapis.com/x", "https:///x", "ftp://example.com/x", "javascript:1",
	} {
		if code := post(bad); code != http.StatusBadRequest {
			t.Errorf("endpoint %q: status = %d, want 400", bad, code)
		}
	}
	if code := post("https://fcm.googleapis.com/fcm/send/abc"); code != http.StatusOK {
		t.Errorf("valid endpoint: status = %d, want 200", code)
	}
}

func TestPushSubscriptionSizeAndCountLimits(t *testing.T) {
	h, _ := newAccountOwnershipTestHandler(t)
	post := func(endpoint string) int {
		body := `{"endpoint":"` + endpoint + `","keys":{"p256dh":"k","auth":"a"}}`
		req := httptest.NewRequest(http.MethodPost, "/api/push/subscription", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.handleSavePushSubscription(rec, ownerRequest(req))
		return rec.Code
	}
	if code := post("https://fcm.googleapis.com/" + strings.Repeat("a", 2100)); code != http.StatusBadRequest {
		t.Errorf("oversized endpoint: status = %d, want 400", code)
	}
	for i := 0; i < 20; i++ {
		if code := post("https://fcm.googleapis.com/fcm/send/" + strconv.Itoa(i)); code != http.StatusOK {
			t.Fatalf("subscription %d: status = %d, want 200", i, code)
		}
	}
	if code := post("https://fcm.googleapis.com/fcm/send/21"); code != http.StatusConflict {
		t.Errorf("subscription over the cap: status = %d, want 409", code)
	}
	if code := post("https://fcm.googleapis.com/fcm/send/3"); code != http.StatusOK {
		t.Errorf("re-registering an existing endpoint at the cap: status = %d, want 200", code)
	}
}
