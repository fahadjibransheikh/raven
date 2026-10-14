package mailauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"golang.org/x/oauth2"
)

type rewriteHostTransport struct{ target *url.URL }

func (t rewriteHostTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = t.target.Scheme, t.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

func TestGetGoogleAccountInfoReadsPicture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/userinfo" || r.Header.Get("Authorization") != "Bearer access" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"sub":"s","email":"me@example.com","name":"Me","picture":"https://lh3.googleusercontent.com/a/x=s96-c"}`))
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rewriteHostTransport{target}})

	svc := New(&Config{GoogleClient: &oauth2.Config{}}, nil, nil)
	info, err := svc.GetGoogleAccountInfo(ctx, &oauth2.Token{AccessToken: "access", TokenType: "Bearer"})
	if err != nil {
		t.Fatalf("GetGoogleAccountInfo() error = %v", err)
	}
	if info.Picture != "https://lh3.googleusercontent.com/a/x=s96-c" || info.Email != "me@example.com" {
		t.Fatalf("info = %+v, want picture and email decoded", info)
	}
}

func TestGetGoogleAccountInfoWithoutGoogleOAuth(t *testing.T) {
	if _, err := New(&Config{}, nil, nil).GetGoogleAccountInfo(context.Background(), &oauth2.Token{AccessToken: "access"}); err == nil {
		t.Fatal("GetGoogleAccountInfo() error = nil, want not configured")
	}
}
