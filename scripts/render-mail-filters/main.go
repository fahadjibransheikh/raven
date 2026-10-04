// Command render-mail-filters prints the mail list toolbar and filters popover HTML so the
// JS tests in tests/js run against the real templ markup, not a copy of it.
package main

import (
	"context"
	"log"
	"os"

	"github.com/cristianadrielbraun/gofer/internal/models"
	"github.com/cristianadrielbraun/gofer/internal/views"
)

func main() {
	if err := views.MailListToolbar(nil, "inbox", "cards", models.EmailFilters{}).Render(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
	if err := views.MailFiltersPopover(nil).Render(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}
