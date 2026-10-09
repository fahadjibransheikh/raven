package views

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

var chipRE = regexp.MustCompile(`<kbd class="kbd" data-shortcut="([^"]+)">([^<]*)</kbd>`)

func TestReaderToolbarShowsKeyChipsAndKeyedTooltips(t *testing.T) {
	var b bytes.Buffer
	email := &models.Email{ID: "m1", FolderRole: "inbox"}
	if err := MailViewHeader(email).Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	chips := map[string]string{}
	for _, m := range chipRE.FindAllStringSubmatch(out, -1) {
		chips[m[1]] = m[2]
	}
	for _, id := range []string{"archive", "move"} {
		if chips[id] != shortcutKeys[id] || chips[id] == "" {
			t.Errorf("chip %q = %q, want %q", id, chips[id], shortcutKeys[id])
		}
	}
	for _, want := range []string{"Archive (E)", "Delete (#)", "Star (S)", "Mark as read (U)", ">Archive</span>"} {
		if !strings.Contains(out, want) {
			t.Errorf("toolbar missing %q", want)
		}
	}
}

func TestReplyBarPicksReplyAllOnlyWithOtherRecipients(t *testing.T) {
	one := &models.Email{To: []models.Contact{{Email: "me@x"}}}
	many := &models.Email{To: []models.Contact{{Email: "me@x"}, {Email: "you@x"}}}
	if got := replyBarInputLabel(one); got != "Reply…" {
		t.Errorf("one recipient label = %q", got)
	}
	if got := replyBarInputLabel(many); got != "Reply all…" || replyBarInputMode(many) != "reply-all" {
		t.Errorf("many recipients label = %q mode = %q", got, replyBarInputMode(many))
	}
	var b bytes.Buffer
	if err := MailViewReplyBar(many).Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"mail-view-reply-input", "mail-view-hints", `data-shortcut="reply"`, `data-shortcut="forward"`, `data-shortcut="archive"`, "hint-mac", "hint-other"} {
		if !strings.Contains(out, want) {
			t.Errorf("reply bar missing %q", want)
		}
	}
}
