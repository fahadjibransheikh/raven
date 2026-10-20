package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/models"
)

func renderContactReadOnly(t *testing.T, contact models.Contact, syncQueued bool) string {
	t.Helper()
	var out bytes.Buffer
	if err := ContactReadOnlyView(contact, nil, syncQueued, nil).Render(context.Background(), &out); err != nil {
		t.Fatalf("ContactReadOnlyView.Render() error = %v", err)
	}
	return out.String()
}

func TestContactReadOnlyLeavesOutEmptyFields(t *testing.T) {
	html := renderContactReadOnly(t, models.Contact{ID: "c1", Name: "Jane", Email: "jane@example.com"}, false)
	if strings.Contains(html, ">Empty<") {
		t.Fatalf("empty fields should be left out: %s", html)
	}
	for _, label := range []string{">Phone<", ">Title<", ">Organization<", ">Notes<"} {
		if strings.Contains(html, label) {
			t.Fatalf("empty %s field rendered", label)
		}
	}
	if !strings.Contains(html, "jane@example.com") || !strings.Contains(html, ">Name<") {
		t.Fatal("filled fields must still show")
	}
}

func TestContactReadOnlyShowsSyncOnlyWhenTheContactSyncs(t *testing.T) {
	if html := renderContactReadOnly(t, models.Contact{ID: "c1", Name: "Jane"}, false); strings.Contains(html, "Raven Sync") {
		t.Fatal("a contact that doesn't sync should not show the sync section")
	}
	if html := renderContactReadOnly(t, models.Contact{ID: "c1", Name: "Jane", SyncError: "CardDAV 412"}, false); !strings.Contains(html, "Raven Sync") || !strings.Contains(html, "CardDAV 412") {
		t.Fatal("a sync error must keep the section and show the error")
	}
}

func TestMailTableRowNamesItsAccount(t *testing.T) {
	var out bytes.Buffer
	accounts := []models.Account{{ID: "acc", Name: "Work"}}
	if err := MailListTableItem(accounts, models.Email{ID: "m1", AccountID: "acc"}, 0, nil, "name").Render(context.Background(), &out); err != nil {
		t.Fatalf("MailListTableItem.Render() error = %v", err)
	}
	html := out.String()
	if !strings.Contains(html, `<span class="sr-only">Work, </span>`) || !strings.Contains(html, `title="Work"`) {
		t.Fatalf("table row should name its account, not just colour it: %s", html)
	}
}
