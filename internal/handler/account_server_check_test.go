package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/auth"
)

func serverCheck(t *testing.T, h *Handler, values url.Values) []accountServerNote {
	t.Helper()
	rec := httptest.NewRecorder()
	h.handleAccountServerCheck(rec, httptest.NewRequest(http.MethodGet, "/api/accounts/server-check?"+values.Encode(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %q", rec.Code, rec.Body.String())
	}
	var body struct {
		Notes []accountServerNote `json:"notes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Notes
}

// The account form learns that Raven will refuse a server before the account
// is saved and tested.
func TestAccountServerCheckReportsRefusedServersWhileTyping(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	h.auth = auth.NewManager(&auth.Config{Enabled: true, Mode: auth.ModeManaged, BaseURL: "https://raven.example"}, db)
	check := func(host, port, tls string) []accountServerNote {
		return serverCheck(t, h, url.Values{"protocol": {"imap"}, "host": {host}, "port": {port}, "tls_mode": {tls}})
	}

	if notes := check("8.8.8.8", "993", "tls"); len(notes) != 0 {
		t.Fatalf("public server notes = %#v", notes)
	}
	for _, invalid := range [][3]string{{"", "993", "tls"}, {"127.0.0.1", "", "tls"}, {"127.0.0.1", "99999", "tls"}, {"http://127.0.0.1", "993", "tls"}} {
		if notes := check(invalid[0], invalid[1], invalid[2]); len(notes) != 0 {
			t.Fatalf("incomplete input %q produced notes %#v", invalid, notes)
		}
	}

	notes := check("192.168.1.10", "993", "tls")
	if len(notes) != 1 || notes[0].Level != "blocked" || notes[0].Link != "" ||
		!strings.Contains(notes[0].Text, "private network") || !strings.Contains(notes[0].Text, "Ask your Raven administrator") || !strings.Contains(notes[0].Text, "192.168.1.10:993") {
		t.Fatalf("unapproved private server in managed mode = %#v", notes)
	}

	if err := db.AddPrivateTargetException(t.Context(), "imap", "192.168.1.10", 993, "administrator"); err != nil {
		t.Fatal(err)
	}
	notes = check("192.168.1.10", "993", "plaintext")
	if len(notes) != 2 || notes[0].Level != "approved" || notes[1].Level != "blocked" || !strings.Contains(notes[1].Text, "without encryption") {
		t.Fatalf("approved private server over unapproved plaintext = %#v", notes)
	}
	if err := db.AddPlaintextTransportException(t.Context(), "imap", "192.168.1.10", 993, "administrator"); err != nil {
		t.Fatal(err)
	}
	notes = check("192.168.1.10", "993", "plaintext")
	if len(notes) != 2 || notes[0].Level != "approved" || notes[1].Level != "approved" {
		t.Fatalf("fully approved server = %#v", notes)
	}
	// An exception approves one exact port.
	if notes := check("192.168.1.10", "143", "tls"); len(notes) != 1 || notes[0].Level != "blocked" {
		t.Fatalf("other port on an approved server = %#v", notes)
	}
}

func TestAccountServerCheckLinksWhereThePersonCanApprove(t *testing.T) {
	for _, tc := range []struct {
		mode auth.Mode
		link string
	}{
		{auth.ModeOpen, "/settings/security#mail-security"},
		{auth.ModePersonal, "/settings/security#mail-security"},
		{auth.ModeManaged, ""},
	} {
		h, db := newAccountOwnershipTestHandler(t)
		h.auth = auth.NewManager(&auth.Config{Enabled: tc.mode != auth.ModeOpen, Mode: tc.mode, BaseURL: "https://raven.example"}, db)
		notes := serverCheck(t, h, url.Values{"protocol": {"smtp"}, "host": {"127.0.0.1"}, "port": {"25"}, "tls_mode": {"starttls"}})
		if len(notes) != 1 || notes[0].Link != tc.link {
			t.Fatalf("%s mode notes = %#v, want link %q", tc.mode, notes, tc.link)
		}
		if tc.link != "" && !strings.Contains(notes[0].Text, "SMTP private target exception for 127.0.0.1:25 under Mail security") {
			t.Fatalf("%s mode note = %q", tc.mode, notes[0].Text)
		}
	}
}

// Calendar and contacts server addresses get the same note as mail servers.
func TestAccountServerCheckReportsRefusedDAVAddresses(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)
	h.auth = auth.NewManager(&auth.Config{Enabled: true, Mode: auth.ModeManaged, BaseURL: "https://raven.example"}, db)
	check := func(address string) []accountServerNote {
		return serverCheck(t, h, url.Values{"url": {address}})
	}

	for _, address := range []string{"https://8.8.8.8/dav/", "not a url", "ftp://192.168.1.20/", "https:///dav"} {
		if notes := check(address); len(notes) != 0 {
			t.Fatalf("%q produced notes %#v", address, notes)
		}
	}
	notes := check("https://192.168.1.20/dav/")
	if len(notes) != 1 || notes[0].Level != "blocked" || !strings.Contains(notes[0].Text, "HTTPS private target exception for 192.168.1.20:443") {
		t.Fatalf("unapproved private DAV server = %#v", notes)
	}
	if notes := check("http://192.168.1.20:8008/dav/"); len(notes) != 1 || !strings.Contains(notes[0].Text, "HTTP private target exception for 192.168.1.20:8008") {
		t.Fatalf("private DAV server on its own port = %#v", notes)
	}
	if err := db.AddPrivateTargetException(t.Context(), "https", "192.168.1.20", 443, "administrator"); err != nil {
		t.Fatal(err)
	}
	if notes := check("https://192.168.1.20/dav/"); len(notes) != 1 || notes[0].Level != "approved" {
		t.Fatalf("approved private DAV server = %#v", notes)
	}
}
