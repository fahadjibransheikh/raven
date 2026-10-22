package notifications

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// webpush-go adds "mailto:" to any subject that is not an https URL, so a
// configured "mailto:" subject reached Apple as "mailto:mailto:..." and Apple
// rejected every push.
func TestVAPIDSubjectIsNotDoubleMailto(t *testing.T) {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	for _, configured := range []string{"mailto:me@example.com", "me@example.com"} {
		s := New(nil, nil, pub, priv, configured)

		var auth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusCreated)
		}))
		key, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		secret := make([]byte, 16)
		rand.Read(secret)
		resp, err := webpush.SendNotification([]byte("x"), &webpush.Subscription{
			Endpoint: server.URL,
			Keys: webpush.Keys{
				P256dh: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
				Auth:   base64.RawURLEncoding.EncodeToString(secret),
			},
		}, &webpush.Options{Subscriber: s.vapidSubject, VAPIDPublicKey: pub, VAPIDPrivateKey: priv, TTL: 60})
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		token := strings.TrimPrefix(strings.SplitN(auth, ",", 2)[0], "vapid t=")
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			t.Fatalf("unexpected Authorization header %q", auth)
		}
		claims, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			t.Fatal(err)
		}
		var c struct{ Sub string }
		if err := json.Unmarshal(claims, &c); err != nil {
			t.Fatal(err)
		}
		if c.Sub != "mailto:me@example.com" {
			t.Fatalf("subject %q sent as %q, want mailto:me@example.com", configured, c.Sub)
		}
	}
}
