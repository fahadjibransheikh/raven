package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

func TestSenderLabelBothShowsAddressAsSuffixWithoutBrackets(t *testing.T) {
	var out bytes.Buffer
	c := models.Contact{Name: "Expedia.com", Email: "expedia@eg.expedia.com"}
	if err := SenderLabel(c, "both").Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if !strings.Contains(html, "Expedia.com") || !strings.Contains(html, `text-subtle-foreground">expedia@eg.expedia.com</span>`) {
		t.Fatalf("both mode should render name then muted address: %s", html)
	}
	if strings.Contains(html, "&lt;") || strings.Contains(html, "<expedia") {
		t.Fatalf("angle brackets leaked: %s", html)
	}
	out.Reset()
	_ = SenderLabel(c, "name").Render(context.Background(), &out)
	if strings.Contains(out.String(), "expedia@") {
		t.Fatalf("name mode must not show the address: %s", out.String())
	}
}

func TestMailListToolbarWrapsBulkActionsForSelectionOnlyVisibility(t *testing.T) {
	var out bytes.Buffer
	if err := MailListToolbar(nil, "inbox", "cards", models.EmailFilters{}).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	start := strings.Index(html, `mail-list-bulk-actions`)
	if start < 0 {
		t.Fatal("bulk action wrapper missing")
	}
	end := strings.Index(html[start:], "data-mail-card-layout-trigger-wrapper")
	if end < 0 {
		t.Fatal("layout trigger not found after the bulk wrapper")
	}
	wrapped := html[start : start+end]
	for _, action := range []string{`"archive"`, `"delete"`, `"read"`} {
		if !strings.Contains(wrapped, "data-mail-selection-action="+action) {
			t.Errorf("%s not inside the bulk wrapper", action)
		}
	}
	for _, kept := range []string{"data-mail-select-all", "data-mail-unread-toggle", "data-mail-list-view-toggle", `aria-label="Sort messages"`} {
		if strings.Contains(wrapped, kept) {
			t.Errorf("%s must stay outside the bulk wrapper", kept)
		}
	}
}
