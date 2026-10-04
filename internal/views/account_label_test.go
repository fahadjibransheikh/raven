package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

func TestAccountDisplayLabelFallbackOrder(t *testing.T) {
	for _, test := range []struct {
		name    string
		account models.Account
		want    string
	}{
		{"label wins", models.Account{Label: " Work ", Name: "Fahad Sheikh", Email: "a@example.com"}, "Work"},
		{"name when no label", models.Account{Label: "  ", Name: "Fahad Sheikh", Email: "a@example.com"}, "Fahad Sheikh"},
		{"email when neither", models.Account{Email: "a@example.com"}, "a@example.com"},
		{"empty when nothing", models.Account{}, ""},
	} {
		if got := test.account.DisplayLabel(); got != test.want {
			t.Errorf("%s: DisplayLabel() = %q, want %q", test.name, got, test.want)
		}
	}
}

func TestAccountLabelUsedByUIHelpers(t *testing.T) {
	accounts := []models.Account{
		{ID: "a", Name: "Fahad Sheikh", Email: "a@example.com", Label: "Work"},
		{ID: "b", Name: "Fahad Sheikh", Email: "b@example.com"},
	}
	if got := mailListAccountDisplay(accounts, "a"); got != "Work" {
		t.Errorf("mailListAccountDisplay(a) = %q, want Work", got)
	}
	if got := mailListAccountDisplay(accounts, "b"); got != "Fahad Sheikh" {
		t.Errorf("mailListAccountDisplay(b) = %q, want name fallback", got)
	}
	if got := contactSidebarAccountName(accounts[0]); got != "Work" {
		t.Errorf("contactSidebarAccountName = %q, want Work", got)
	}
	if got := composeAccountName(models.Account{Email: "x@example.com"}); got != "" {
		t.Errorf("composeAccountName without label/name = %q, want empty (no email fallback)", got)
	}
}

func TestComposeFromPickerShowsLabelButSubmitsAccountID(t *testing.T) {
	var out bytes.Buffer
	accounts := []models.Account{{ID: "acct-1", Name: "Fahad Sheikh", Label: "Work", Email: "a@example.com"}}
	if err := ComposeDialog(accounts).Render(context.Background(), &out); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := out.String()
	if !strings.Contains(html, `data-account-name="Work"`) || !strings.Contains(html, "Work &lt;a@example.com&gt;") {
		t.Fatalf("compose From picker does not show the label: %s", html)
	}
	if !strings.Contains(html, `name="account_id"`) || strings.Contains(html, `name="label"`) {
		t.Fatalf("compose form must submit account_id only")
	}
}
