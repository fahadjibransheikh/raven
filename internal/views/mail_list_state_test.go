package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/models"
)

func renderCard(t *testing.T, email models.Email) string {
	t.Helper()
	var out bytes.Buffer
	if err := MailListCardItem(nil, email, 0, nil, "name").Render(context.Background(), &out); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	return out.String()
}

func TestMailListCardUnreadCarriesWeightAndLabel(t *testing.T) {
	unread := renderCard(t, models.Email{ID: "m1", Subject: "Hi", From: models.Contact{Name: "Ana"}})
	if !strings.Contains(unread, "font-semibold text-foreground") {
		t.Fatalf("unread sender is not semibold:\n%s", unread)
	}
	if !strings.Contains(unread, `<span class="sr-only" data-mail-unread-label>Unread, </span>`) {
		t.Fatalf("unread row has no screen-reader label:\n%s", unread)
	}
	read := renderCard(t, models.Email{ID: "m2", IsRead: true, Subject: "Hi", From: models.Contact{Name: "Ana"}})
	if strings.Contains(read, "data-mail-unread-label") || strings.Contains(read, "font-semibold text-foreground") {
		t.Fatalf("read row looks unread:\n%s", read)
	}
}

func TestMailListUnstarredStarIsNamedAndFocusVisible(t *testing.T) {
	html := renderCard(t, models.Email{ID: "m1", IsRead: true})
	if !strings.Contains(html, `aria-label="Star"`) || !strings.Contains(html, "group-focus-within:opacity-100") {
		t.Fatalf("unstarred star button is unnamed or hidden on focus:\n%s", html)
	}
}

func TestMailListSentRowsLeadWithRecipient(t *testing.T) {
	accounts := []models.Account{{ID: "a1", Folders: []models.Folder{{ID: "f-sent", Role: "Sent"}, {ID: "f-in", Role: "inbox"}}}}
	email := models.Email{ID: "m1", IsRead: true, From: models.Contact{Name: "Me Myself"}, To: []models.Contact{{Name: "Bob Builder"}}}

	for _, folderID := range []string{"sent", "f-sent", "drafts"} {
		html := renderCard(t, mailListRowEmail(email, accounts, folderID))
		if !strings.Contains(html, "Bob Builder") || strings.Contains(html, "Me Myself") {
			t.Fatalf("%s row should show the recipient, not the sender:\n%s", folderID, html)
		}
	}
	for _, folderID := range []string{"inbox", "f-in", "unknown-id"} {
		html := renderCard(t, mailListRowEmail(email, accounts, folderID))
		if !strings.Contains(html, "Me Myself") {
			t.Fatalf("%s row should show the sender:\n%s", folderID, html)
		}
	}
	// No recipients known: fall back to the sender rather than an empty "To".
	noTo := mailListRowEmail(models.Email{ID: "m2", From: models.Contact{Name: "Me Myself"}}, accounts, "sent")
	if !strings.Contains(renderCard(t, noTo), "Me Myself") {
		t.Fatal("sent row without recipients lost its sender")
	}
	// A row that already knows its role (e.g. trash) is not relabelled.
	if got := mailListRowEmail(models.Email{FolderRole: "trash"}, accounts, "sent").FolderRole; got != "trash" {
		t.Fatalf("FolderRole = %q, want trash", got)
	}
}

func TestMailPreviewTextDropsRuleRuns(t *testing.T) {
	cases := map[string]string{
		"_______ agencies looking": "agencies looking",
		"Hi ---- there ==== you":   "Hi there you",
		"a - b -- c":               "a - b -- c",
		"":                         "",
	}
	for in, want := range cases {
		if got := mailPreviewText(in); got != want {
			t.Errorf("mailPreviewText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMailFolderCountTitle(t *testing.T) {
	if got := mailFolderCountTitle(1); got != "1 message" {
		t.Fatalf("got %q", got)
	}
	if got := mailFolderCountTitle(19); got != "19 messages" {
		t.Fatalf("got %q", got)
	}
}
