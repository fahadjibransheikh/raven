package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

func TestComposeFromPickerGroupsIdentitiesByAccount(t *testing.T) {
	accounts := []models.Account{
		{ID: "a1", Name: "Fahad Sheikh", Label: "Work", Email: "me@example.com", Identities: []models.AccountIdentity{
			{AccountID: "a1", Email: "me@example.com", Source: models.IdentitySourcePrimary},
			{AccountID: "a1", Email: "sales@example.com", Name: "Sales Team", Source: models.IdentitySourceManual, IsDefault: true},
			{AccountID: "a1", Email: "info@example.com", Source: models.IdentitySourceProvider},
		}},
		{ID: "a2", Name: "Fahad Home", Email: "home@example.com", Identities: []models.AccountIdentity{
			{AccountID: "a2", Email: "home@example.com", Source: models.IdentitySourcePrimary, IsDefault: true},
		}},
	}
	var out bytes.Buffer
	if err := ComposeDialog(accounts).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{
		`name="from_email" id="compose-from-email" value="sales@example.com"`, // default identity of the first account
		`Sales Team &lt;sales@example.com&gt;`,
		`Fahad Sheikh &lt;info@example.com&gt;`, // identity without a name falls back to the account name, not the label
		`Fahad Home &lt;home@example.com&gt;`,   // single-identity account reads as before
		`data-account-email="info@example.com"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("picker missing %q", want)
		}
	}
	// Heading only for the account with several identities, and it is the label.
	if got := strings.Count(html, "data-compose-account-heading"); got != 1 {
		t.Errorf("group headings = %d, want 1 (multi-identity account only)", got)
	}
	if !strings.Contains(html, "Work\n") && !strings.Contains(html, ">Work<") && !strings.Contains(html, "> Work") {
		t.Errorf("heading should be the account label")
	}
	if !strings.Contains(html, "Fahad Sheikh &lt;me@example.com&gt;") {
		t.Errorf("primary identity of a multi-identity account should show the account name")
	}
	if strings.Count(html, `data-compose-account-selected="true"`) != 1 {
		t.Errorf("exactly one item must be selected, html has %d", strings.Count(html, `data-compose-account-selected="true"`))
	}
}

func TestComposeFromPickerSingleIdentityAccountsUnchanged(t *testing.T) {
	var out bytes.Buffer
	accounts := []models.Account{{ID: "a1", Name: "Fahad", Label: "Work", Email: "a@example.com"}}
	if err := ComposePane(accounts).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if !strings.Contains(html, "Work &lt;a@example.com&gt;") || !strings.Contains(html, `id="compose-pane-from-email" value="a@example.com"`) {
		t.Fatalf("single-identity picker changed: %s", html)
	}
	if strings.Contains(html, "data-compose-account-heading") {
		t.Fatal("single-identity account must not get a group heading")
	}
}
