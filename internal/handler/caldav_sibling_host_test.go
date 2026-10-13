package handler

import "testing"

func TestResolveCalDAVHrefAllowsOnlySameRegisteredDomainHTTPSHosts(t *testing.T) {
	base := "https://caldav.icloud.com/"
	for _, href := range []string{
		"https://p112-caldav.icloud.com/1836625426/calendars/home/",
		"https://P112-CalDAV.icloud.com:443/1836625426/principal/",
		"/1836625426/principal/",
	} {
		if _, err := resolveCalDAVHref(base, href); err != nil {
			t.Fatalf("resolveCalDAVHref(%q, %q) = %v, want allowed", base, href, err)
		}
	}
	for _, tc := range []struct{ base, href string }{
		{base, "https://p112-caldav.evil-icloud.com/calendars/"},
		{base, "https://icloud.com.evil.example/calendars/"},
		{base, "http://p112-caldav.icloud.com/calendars/"},
		{base, "https://p112-caldav.icloud.com:8443/calendars/"},
		{"http://caldav.example.com/", "http://other.example.com/calendars/"},
		{"https://203.0.113.10/", "https://203.0.113.11/calendars/"},
		// github.io is a public suffix: different users' sites are not siblings.
		{"https://alice.github.io/dav/", "https://bob.github.io/dav/"},
	} {
		if _, err := resolveCalDAVHref(tc.base, tc.href); err == nil {
			t.Fatalf("resolveCalDAVHref(%q, %q) allowed, want refused", tc.base, tc.href)
		}
	}
}
