package handler

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestUnsubscribeAddrBlocked(t *testing.T) {
	blocked := []string{"127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "fd00::1", "169.254.169.254",
		"fe80::1", "0.0.0.0", "::", "224.0.0.1", "ff02::1", "100.64.0.1", "100.127.255.254", "0.1.2.3", "::ffff:127.0.0.1", "::ffff:10.0.0.1"}
	for _, s := range blocked {
		if !unsubscribeAddrBlocked(netip.MustParseAddr(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"93.184.216.34", "8.8.8.8", "100.128.0.1", "2606:4700:4700::1111"} {
		if unsubscribeAddrBlocked(netip.MustParseAddr(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestProductionUnsubscribeClientRejectsLoopback(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	// srv listens on 127.0.0.1; "localhost" resolves to it as well.
	for _, target := range []string{srv.URL, strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)} {
		if err := unsubscribeOneClick(t.Context(), productionUnsubscribeClient(), target); err == nil || !strings.Contains(err.Error(), "refusing to connect") {
			t.Errorf("%s: err = %v, want the dial guard to refuse", target, err)
		}
	}
	if hits.Load() != 0 {
		t.Errorf("server was reached %d times", hits.Load())
	}
	if err := unsubscribeOneClick(t.Context(), productionUnsubscribeClient(), "http://example.com/u"); err == nil {
		t.Error("plain http accepted")
	}
}

func TestUnsubscribeRedirectIsRevalidatedAtDialTime(t *testing.T) {
	var internalHits atomic.Int32
	internal := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { internalHits.Add(1) }))
	defer internal.Close()
	first := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusTemporaryRedirect)
	}))
	defer first.Close()
	// Both servers are on loopback; allow only the first dial, as if the redirect target
	// resolved to an internal address.
	var dials atomic.Int32
	client := newUnsubscribeClient(func(netip.Addr) bool { return dials.Add(1) > 1 })
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	if err := unsubscribeOneClick(t.Context(), client, first.URL); err == nil || internalHits.Load() != 0 {
		t.Errorf("err = %v, internal hits = %d", err, internalHits.Load())
	}
}

func TestUnsubscribeOneClickRequestShapeAndStatus(t *testing.T) {
	var gotBody, gotType, gotUA string
	var gotCookies int
	redirects := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			buf := make([]byte, 100)
			n, _ := r.Body.Read(buf)
			gotBody, gotType, gotUA, gotCookies = string(buf[:n]), r.Header.Get("Content-Type"), r.UserAgent(), len(r.Cookies())
			http.SetCookie(w, &http.Cookie{Name: "a", Value: "b"})
			w.WriteHeader(http.StatusAccepted)
		case "/fail":
			http.Error(w, "nope", http.StatusForbidden)
		default: // endless redirect loop
			redirects++
			http.Redirect(w, r, "/loop", http.StatusTemporaryRedirect)
		}
	}))
	defer srv.Close()
	client := newUnsubscribeClient(func(netip.Addr) bool { return false }) // test-only: allow loopback
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	if err := unsubscribeOneClick(t.Context(), client, srv.URL+"/ok"); err != nil {
		t.Fatal(err)
	}
	if gotBody != "List-Unsubscribe=One-Click" || gotType != "application/x-www-form-urlencoded" || gotUA != "Raven" || gotCookies != 0 {
		t.Errorf("body=%q type=%q ua=%q cookies=%d", gotBody, gotType, gotUA, gotCookies)
	}
	if err := unsubscribeOneClick(t.Context(), client, srv.URL+"/fail"); err == nil {
		t.Error("403 treated as success")
	}
	if err := unsubscribeOneClick(t.Context(), client, srv.URL+"/loop"); err == nil || redirects > unsubscribeMaxRedirects+1 {
		t.Errorf("redirect loop: err=%v redirects=%d", err, redirects)
	}
}

func unsubscribeRequest(fixture messageActionOwnershipFixture, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	id := strconv.FormatInt(fixture.victimMessageID, 10)
	req := httptest.NewRequest(http.MethodPost, "/api/messages/"+id+"/unsubscribe", nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	fixture.handler.handleUnsubscribeMessage(rec, as(req))
	return rec
}

func TestHandleUnsubscribeMessageChoosesMethod(t *testing.T) {
	const oneClick = "List-Unsubscribe=One-Click"
	cases := []struct {
		name, header, post string
		wantStatus         int
		wantAction         string
		wantURL            string
	}{
		{"browser only", "<https://news.example.invalid/u?id=1>", "", http.StatusOK, "open", "https://news.example.invalid/u?id=1"},
		{"mailto", "<mailto:unsub@news.example.invalid?subject=bye>", "", http.StatusOK, "done", ""},
		{"nothing", "", "", http.StatusConflict, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newMessageActionOwnershipFixture(t)
			if _, err := fixture.db.Write().ExecContext(t.Context(),
				`UPDATE messages SET list_unsubscribe = ?, list_unsubscribe_post = ? WHERE id = ?`, tc.header, tc.post, fixture.victimMessageID); err != nil {
				t.Fatal(err)
			}
			rec := unsubscribeRequest(fixture, ownerRequest)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
			}
			if tc.wantAction == "" {
				return
			}
			var got map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got["action"] != tc.wantAction || got["url"] != tc.wantURL {
				t.Errorf("reply = %v", got)
			}
			if tc.name == "mailto" {
				var queued int
				if err := fixture.db.Read().QueryRow(`SELECT COUNT(*) FROM outgoing_sends WHERE account_id = 'victim-account'`).Scan(&queued); err != nil || queued != 1 {
					t.Errorf("queued sends = %d, %v", queued, err)
				}
			}
		})
	}

	t.Run("one-click", func(t *testing.T) {
		fixture := newMessageActionOwnershipFixture(t)
		var body string
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b := make([]byte, 64)
			n, _ := r.Body.Read(b)
			body = string(b[:n])
		}))
		defer srv.Close()
		client := newUnsubscribeClient(func(netip.Addr) bool { return false })
		client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		fixture.handler.unsubscribeClient = client
		if _, err := fixture.db.Write().ExecContext(t.Context(),
			`UPDATE messages SET list_unsubscribe = ?, list_unsubscribe_post = ? WHERE id = ?`,
			"<"+srv.URL+"/u>, <mailto:u@x.invalid>", oneClick, fixture.victimMessageID); err != nil {
			t.Fatal(err)
		}
		rec := unsubscribeRequest(fixture, ownerRequest)
		if rec.Code != http.StatusOK || body != oneClick || !strings.Contains(rec.Body.String(), `"action":"done"`) {
			t.Errorf("status=%d body=%q server saw %q", rec.Code, rec.Body.String(), body)
		}
		var queued int
		_ = fixture.db.Read().QueryRow(`SELECT COUNT(*) FROM outgoing_sends`).Scan(&queued)
		if queued != 0 {
			t.Errorf("mailto fallback used although one-click succeeded")
		}
	})

	t.Run("one-click to a private address is refused with the production client", func(t *testing.T) {
		fixture := newMessageActionOwnershipFixture(t)
		if _, err := fixture.db.Write().ExecContext(t.Context(),
			`UPDATE messages SET list_unsubscribe = '<https://127.0.0.1:9/u>', list_unsubscribe_post = ? WHERE id = ?`, oneClick, fixture.victimMessageID); err != nil {
			t.Fatal(err)
		}
		if rec := unsubscribeRequest(fixture, ownerRequest); rec.Code != http.StatusBadGateway {
			t.Errorf("status = %d, want 502", rec.Code)
		}
	})

	t.Run("foreign user", func(t *testing.T) {
		fixture := newMessageActionOwnershipFixture(t)
		if _, err := fixture.db.Write().ExecContext(t.Context(),
			`UPDATE messages SET list_unsubscribe = '<https://x.example.invalid/u>' WHERE id = ?`, fixture.victimMessageID); err != nil {
			t.Fatal(err)
		}
		if rec := unsubscribeRequest(fixture, attackerRequest); rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})
}

func TestUnsubscribeMailtoSendsFromTheAddressedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name       string
		recipient  string // "" = message addressed to no known identity
		wantAlias  bool
		recipientK string
	}{
		{"alias in To", "alias@example.com", true, "to"},
		{"alias in Cc", "Alias@Example.com", true, "cc"},
		{"no identity matches", "list@elsewhere.invalid", false, "to"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newMessageActionOwnershipFixture(t)
			ctx := t.Context()
			if _, err := fixture.db.AddManualIdentity(ctx, "owner", "victim-account", "alias@example.com", ""); err != nil {
				t.Fatal(err)
			}
			var accountEmail string
			if err := fixture.db.Read().QueryRow(`SELECT email_address FROM accounts WHERE id = 'victim-account'`).Scan(&accountEmail); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.db.Write().ExecContext(ctx,
				`UPDATE messages SET list_unsubscribe = '<mailto:unsub@news.example.invalid>' WHERE id = ?`, fixture.victimMessageID); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.db.Write().ExecContext(ctx,
				`INSERT INTO message_recipients (message_id, kind, name, email) VALUES (?, ?, '', ?)`, fixture.victimMessageID, tc.recipientK, tc.recipient); err != nil {
				t.Fatal(err)
			}
			if rec := unsubscribeRequest(fixture, ownerRequest); rec.Code != http.StatusOK {
				t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
			}
			// The SMTP envelope is always the account's own address; the From header carries the identity.
			var mime []byte
			if err := fixture.db.Read().QueryRow(`SELECT mime_data FROM outgoing_sends WHERE account_id = 'victim-account'`).Scan(&mime); err != nil {
				t.Fatal(err)
			}
			parsed, err := mail.ReadMessage(bytes.NewReader(mime))
			if err != nil {
				t.Fatal(err)
			}
			fromAddr, err := mail.ParseAddress(parsed.Header.Get("From"))
			if err != nil {
				t.Fatal(err)
			}
			from := fromAddr.Address
			want := accountEmail
			if tc.wantAlias {
				want = "alias@example.com"
			}
			if !strings.EqualFold(from, want) {
				t.Errorf("From = %q, want %q", from, want)
			}
		})
	}
}
