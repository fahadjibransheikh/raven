package views

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

func TestThreadTargetID(t *testing.T) {
	item := func(id string, read bool) models.ThreadItem { return models.ThreadItem{ID: id, IsRead: read} }
	cases := []struct {
		name   string
		thread []models.ThreadItem
		want   string
	}{
		{"empty", nil, ""},
		{"all read opens newest", []models.ThreadItem{item("a", true), item("b", true), item("c", true)}, "c"},
		{"oldest unread wins", []models.ThreadItem{item("a", true), item("b", false), item("c", false)}, "b"},
		{"only newest unread", []models.ThreadItem{item("a", true), item("b", true), item("c", false)}, "c"},
	}
	for _, c := range cases {
		if got := ThreadTargetID(c.thread); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestThreadNewestFirstDefault(t *testing.T) {
	for _, c := range []struct {
		settings map[string]string
		want     bool
	}{
		{nil, true},
		{map[string]string{"thread_order": ""}, true},
		{map[string]string{"thread_order": "newest_first"}, true},
		{map[string]string{"thread_order": "oldest_first"}, false},
	} {
		if got := ThreadNewestFirst(c.settings); got != c.want {
			t.Errorf("%v: got %v want %v", c.settings, got, c.want)
		}
	}
}

func renderThread(t *testing.T, thread []models.ThreadItem, newestFirst bool) string {
	t.Helper()
	var out bytes.Buffer
	email := &models.Email{ID: thread[0].ID, Subject: "S"}
	if err := MailViewContent(email, thread, newestFirst).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func threadFixture() []models.ThreadItem { // oldest first
	mk := func(id string, read bool) models.ThreadItem {
		return models.ThreadItem{ID: id, IsRead: read, From: models.Contact{Name: "N" + id, Email: id + "@x.io"}}
	}
	return []models.ThreadItem{mk("m1", true), mk("m2", true), mk("m3", true), mk("m4", true)}
}

func TestMailViewThreadNewestFirstExpandsNewestAndUnreadWithoutPin(t *testing.T) {
	th := threadFixture()
	th[1].IsRead = false // m2 unread
	slices.Reverse(th)   // display order: m4 m3 m2 m1
	html := renderThread(t, th, true)
	if strings.Contains(html, "data-thread-target") {
		t.Error("newest-first must not pin a scroll target")
	}
	if strings.Count(html, "<details open") != 2 {
		t.Errorf("want newest and the unread message open, got %d open", strings.Count(html, "<details open"))
	}
	if !ascending(html, "Nm4", "Nm3", "Nm2", "Nm1") {
		t.Error("messages not rendered newest first")
	}
	if strings.Count(html, "data-thread-newest") != 1 {
		t.Error("exactly one message must be the reply target")
	}
}

func TestMailViewThreadOldestFirstMarksTarget(t *testing.T) {
	th := threadFixture()
	html := renderThread(t, th, false)
	if strings.Count(html, "data-thread-target") != 1 || strings.Count(html, "<details open") != 1 {
		t.Fatal("oldest-first needs exactly one target, expanded")
	}
	if !ascending(html, "Nm1", "Nm2", "Nm3", "Nm4") {
		t.Error("messages not rendered oldest first")
	}
	// everything read: the target is the newest message, which is also the reply target
	iT := strings.Index(html, "data-thread-target")
	if iT < 0 || !strings.HasPrefix(html[iT+strings.Index(html[iT:], "Nm"):], "Nm4") {
		t.Error("target should be the newest message")
	}
	if strings.Index(html, "data-thread-newest") < strings.Index(html, "Nm4") {
		t.Error("reply target should be the newest message")
	}
}

func ascending(html string, marks ...string) bool {
	prev := -1
	for _, m := range marks {
		i := strings.Index(html, m)
		if i <= prev {
			return false
		}
		prev = i
	}
	return true
}
