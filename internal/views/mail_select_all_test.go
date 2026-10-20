package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/models"
)

func TestMailListToolbarHasSelectAllMenu(t *testing.T) {
	var out bytes.Buffer
	if err := MailListToolbar(nil, "inbox", "cards", models.EmailFilters{}).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, choice := range []string{"all", "none", "read", "unread", "starred", "unstarred", "invert"} {
		if !strings.Contains(html, `data-mail-select="`+choice+`"`) {
			t.Errorf("select-all menu is missing %q", choice)
		}
	}
	if !strings.Contains(html, "data-mail-select-all") {
		t.Error("select-all checkbox missing")
	}
}

func TestMailListHasSelectAllBanner(t *testing.T) {
	var out bytes.Buffer
	if err := MailList(nil, nil, "inbox", nil, 0, 0, 0, "", "name", "cards", "scroll", models.EmailFilters{}).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{"data-mail-select-all-banner", "data-mail-select-all-banner-action"} {
		if !strings.Contains(html, want) {
			t.Errorf("banner is missing %s", want)
		}
	}
}

func TestMailRowsCarryReadStateForSelectByState(t *testing.T) {
	for _, tc := range []struct {
		read bool
		want string
	}{{true, `data-is-read="true"`}, {false, `data-is-read="false"`}} {
		for _, view := range []string{"cards", "table"} {
			var out bytes.Buffer
			email := models.Email{ID: "m1", IsRead: tc.read, FolderRole: "inbox"}
			var err error
			if view == "cards" {
				err = MailListCardItem(nil, email, 0, nil, "name").Render(context.Background(), &out)
			} else {
				err = MailListTableItem(nil, email, 0, nil, "name").Render(context.Background(), &out)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("%s row missing %s", view, tc.want)
			}
		}
	}
}
