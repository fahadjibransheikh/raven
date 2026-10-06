package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

func renderEmptyList(t *testing.T, accounts []models.Account, folderID string) string {
	t.Helper()
	var out bytes.Buffer
	if err := MailListEmails(accounts, nil, folderID, nil, 0, 0, 0, "name", "cards", "scroll").Render(context.Background(), &out); err != nil {
		t.Fatalf("MailListEmails.Render() error = %v", err)
	}
	return out.String()
}

func TestEmptyMailListWithNoAccountsOffersAddAccount(t *testing.T) {
	html := renderEmptyList(t, nil, "inbox")
	for _, want := range []string{"No accounts yet", `href="/settings/accounts"`, "Add account", "data-no-accounts"} {
		if !strings.Contains(html, want) {
			t.Fatalf("zero-account empty state missing %q: %s", want, html)
		}
	}
	if strings.Contains(html, "Your inbox is empty") {
		t.Fatalf("zero-account empty state still says the inbox is empty")
	}
}

func TestEmptyMailListTextNamesTheFolder(t *testing.T) {
	accounts := []models.Account{{ID: "acc", Folders: []models.Folder{
		{ID: "acc-inbox", Role: "inbox"}, {ID: "acc-spam", Role: "spam"}, {ID: "acc-trash", Role: "trash"}, {ID: "acc-custom", Role: ""},
	}}}
	for folder, want := range map[string]string{
		"acc-inbox": "Your inbox is empty", "acc-spam": "No spam", "acc-trash": "Trash is empty",
		"acc-custom": "This folder is empty", "sent": "No sent messages yet",
	} {
		if html := renderEmptyList(t, accounts, folder); !strings.Contains(html, want) {
			t.Fatalf("folder %q empty state missing %q: %s", folder, want, html)
		}
	}
}
