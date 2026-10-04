// Command render-mail-filters prints the mail list toolbar, sample list rows and filters popover HTML so the
// JS tests in tests/js run against the real templ markup, not a copy of it.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/cristianadrielbraun/gofer/internal/models"
	"github.com/cristianadrielbraun/gofer/internal/views"
)

func main() {
	if err := views.MailListToolbar(nil, "inbox", "cards", models.EmailFilters{}).Render(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
	// Real list rows (a threaded unread card, a read card, an unread table row) for the hover-action tests.
	// The test gives this wrapper id="mail-list-scroll" after app.js boots; app.js starts the virtual list when that id exists at load.
	fmt.Print(`<div data-test-mail-list>`)
	for _, email := range []models.Email{
		{ID: "m1", ThreadID: "t1", ThreadCount: 2, FolderID: "inbox", FolderRole: "inbox"},
		{ID: "m2", IsRead: true, FolderID: "inbox", FolderRole: "inbox"},
	} {
		if err := views.MailListCardItem(nil, email, 0, nil, "name").Render(context.Background(), os.Stdout); err != nil {
			log.Fatal(err)
		}
	}
	if err := views.MailListTableItem(models.Email{ID: "m3", FolderID: "inbox", FolderRole: "inbox"}, 2, nil, "name").Render(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
	fmt.Print(`</div>`)
	if err := views.MailFiltersPopover(nil).Render(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}
