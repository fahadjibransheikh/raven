package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEffectiveTimezone(t *testing.T) {
	req := func(cookie string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "raven_tz", Value: cookie})
		}
		return r
	}
	cases := []struct {
		name, setting, cookie, want string
	}{
		{"local uses cookie", "local", "America%2FNew_York", "America/New_York"},
		{"empty uses cookie", "", "America%2FNew_York", "America/New_York"},
		{"explicit setting wins", "Asia/Karachi", "America%2FNew_York", "Asia/Karachi"},
		{"invalid cookie ignored", "local", "Not%2FAZone", "local"},
		{"no cookie", "local", "", "local"},
	}
	for _, tc := range cases {
		if got := effectiveTimezone(req(tc.cookie), tc.setting); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}

	// 2026-10-09T01:01Z is 9:01 PM the previous evening in New York.
	loc, err := time.LoadLocation(effectiveTimezone(req("America%2FNew_York"), "local"))
	if err != nil {
		t.Fatal(err)
	}
	if got := time.Date(2026, 10, 9, 1, 1, 0, 0, time.UTC).In(loc).Format("3:04 PM"); got != "9:01 PM" {
		t.Errorf("formatted %q, want 9:01 PM", got)
	}
}
