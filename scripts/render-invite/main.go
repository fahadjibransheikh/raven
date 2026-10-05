// Command render-invite prints the invitation card in its found and not-found states as JSON
// ({"found": html, "missing": html}) so tests/js/invite_card.test.js runs against the real templ markup.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"

	"github.com/cristianadrielbraun/gofer/internal/views"
)

func main() {
	base := views.InviteCard{
		EmailID: "42", Kind: "request", Title: "Planning", When: "Wed, Oct 7, 2026, 4:00 PM – 5:00 PM UTC",
		StartISO: "2026-10-07T16:00:00Z", EndISO: "2026-10-07T17:00:00Z", CalendarDate: "2026-10-07",
		Organizer: "Boss (boss@example.com)", Response: "needsAction",
	}
	found, missing := base, base
	found.EventID = 77
	out := map[string]string{}
	for k, c := range map[string]views.InviteCard{"found": found, "missing": missing} {
		var b bytes.Buffer
		if err := views.EmailInviteCard(c).Render(context.Background(), &b); err != nil {
			log.Fatal(err)
		}
		out[k] = b.String()
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		log.Fatal(err)
	}
}
