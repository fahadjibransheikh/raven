package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

func TestMailListRowsRenderQuickActions(t *testing.T) {
	cases := []struct {
		name      string
		email     models.Email
		wantRead  string // data-mail-row-action expected for the read toggle
		notWant   string
		wantLabel string
		delLabel  string
	}{
		{"unread", models.Email{ID: "m1", IsRead: false, FolderRole: "inbox"}, `data-mail-row-action="read"`, `data-mail-row-action="unread"`, `aria-label="Mark as read"`, `aria-label="Delete"`},
		{"read", models.Email{ID: "m2", IsRead: true, FolderRole: "inbox"}, `data-mail-row-action="unread"`, `data-mail-row-action="read"`, `aria-label="Mark as unread"`, `aria-label="Delete"`},
		{"trash", models.Email{ID: "m3", IsRead: true, FolderRole: "trash"}, `data-mail-row-action="unread"`, `data-mail-row-action="read"`, `aria-label="Mark as unread"`, `aria-label="Permanently delete"`},
	}
	for _, view := range []string{"cards", "table"} {
		for _, tc := range cases {
			t.Run(view+"/"+tc.name, func(t *testing.T) {
				var out bytes.Buffer
				var err error
				if view == "cards" {
					err = MailListCardItem(nil, tc.email, 0, nil, "name").Render(context.Background(), &out)
				} else {
					err = MailListTableItem(tc.email, 0, nil, "name").Render(context.Background(), &out)
				}
				if err != nil {
					t.Fatalf("Render() error = %v", err)
				}
				html := out.String()
				want := []string{
					`data-mail-row-action="archive"`, `data-mail-row-action="delete"`, tc.wantRead, tc.wantLabel, tc.delLabel,
					`aria-label="Archive"`, `data-email-id="` + tc.email.ID + `"`, "group-hover:opacity-100", "group-focus-within:opacity-100",
				}
				for _, w := range want {
					if !strings.Contains(html, w) {
						t.Errorf("missing %q in %s", w, html)
					}
				}
				if strings.Contains(html, tc.notWant) {
					t.Errorf("unexpected %q", tc.notWant)
				}
				if n := strings.Count(html, "data-mail-row-action="); n != 3 {
					t.Errorf("row actions = %d, want 3", n)
				}
			})
		}
	}
}
