package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/models"
)

func renderIdentities(t *testing.T, data AccountIdentitiesData) string {
	t.Helper()
	var out bytes.Buffer
	if err := SettingsAccountIdentities(data).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestSettingsAccountIdentitiesSection(t *testing.T) {
	data := AccountIdentitiesData{
		Account: models.Account{ID: "a1", Provider: "gmail", Email: "me@example.com"},
		Identities: []models.AccountIdentity{
			{ID: 1, Email: "me@example.com", Source: "primary", IsDefault: true},
			{ID: 2, Email: "work@example.com", Name: "Work", Source: "provider"},
			{ID: 3, Email: "mine@example.com", Source: "manual"},
		},
		Suggestions: []models.IdentitySuggestion{{Email: "seen@example.com", Count: 4}},
	}
	html := renderIdentities(t, data)
	for _, want := range []string{
		"Sending addresses", "Primary", "From Gmail", "Added by you", "data-identity-default",
		"Refresh from Gmail", `hx-post="/api/accounts/a1/identities/refresh"`,
		`hx-post="/api/accounts/a1/identities/2/default"`, `hx-post="/api/accounts/a1/identities/3/delete"`,
		`data-identity-add-form`, `name="email"`, `seen@example.com`, `data-identity-suggestion-add`, `data-identity-suggestion-dismiss`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("section missing %q", want)
		}
	}
	if strings.Contains(html, `/identities/1/delete`) || strings.Contains(html, `/identities/1/default`) {
		t.Error("primary/default identity must not offer remove or set-default")
	}
	if got := strings.Count(html, "data-identity-default"); got != 1 {
		t.Errorf("default markers = %d, want 1", got)
	}

	data.Account.Provider = "imap"
	data.Suggestions = nil
	data.Error = "That address is already a sending address."
	html = renderIdentities(t, data)
	if strings.Contains(html, "Refresh from Gmail") || strings.Contains(html, "data-identity-suggestions") {
		t.Error("non-Gmail account must not offer Gmail refresh; no suggestions block when empty")
	}
	if !strings.Contains(html, "data-identities-error") {
		t.Error("error not rendered")
	}
}

func TestSettingsAccountCardLoadsIdentitiesSection(t *testing.T) {
	var out bytes.Buffer
	if err := SettingsAccountCard(models.Account{ID: "a1", Email: "me@example.com", Name: "Me", Provider: "imap"}).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `hx-get="/api/accounts/a1/identities"`) {
		t.Error("card does not load the sending addresses section")
	}
}

func TestComposeAccountSelfEmailsIncludesIdentities(t *testing.T) {
	acc := models.Account{Email: "Me@Example.com", Identities: []models.AccountIdentity{{Email: "me@example.com"}, {Email: "work@example.com"}}}
	if got := composeAccountSelfEmails(acc); got != "me@example.com,work@example.com" {
		t.Fatalf("self emails = %q", got)
	}
}

func TestSettingsAccountIdentitiesFoldsTheAddForm(t *testing.T) {
	single := AccountIdentitiesData{
		Account:    models.Account{ID: "a1", Provider: "imap", Email: "me@example.com"},
		Identities: []models.AccountIdentity{{ID: 1, Email: "me@example.com", Source: "primary", IsDefault: true}},
	}
	html := renderIdentities(t, single)
	if !strings.Contains(html, `<details class="mt-3 group/add" data-identity-add>`) || !strings.Contains(html, "Add a sending address") {
		t.Fatalf("add form should be folded behind a disclosure: %s", html)
	}
	if strings.Contains(html, "data-identity-default") {
		t.Fatal("a lone address should not carry a Default badge")
	}
	single.Error = "That address is already added."
	if html := renderIdentities(t, single); !strings.Contains(html, `<details class="mt-3 group/add" open data-identity-add>`) {
		t.Fatalf("an error should leave the add form open: %s", html)
	}
}

func TestSettingsAccountsTabHasNoNestedCardOrRepeatedHeading(t *testing.T) {
	var out bytes.Buffer
	accounts := []models.Account{{ID: "a1", Provider: "imap", Email: "me@example.com", EmailSyncEnabled: true}}
	if err := SettingsAccountsTab(accounts).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, gone := range []string{">Accounts</h3>", "Enabled services", "bg-card rounded-lg border p-6"} {
		if strings.Contains(html, gone) {
			t.Fatalf("accounts tab still renders %q", gone)
		}
	}
	if !strings.Contains(html, "Add Account") || !strings.Contains(html, "me@example.com") {
		t.Fatal("accounts tab lost its account or Add Account")
	}
}
