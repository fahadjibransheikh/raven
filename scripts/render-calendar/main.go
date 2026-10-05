// Command render-calendar prints the calendar sidebar body and main pane HTML so the JS tests in tests/js run
// against the real templ markup, not a copy of it.
package main

import (
	"context"
	"log"
	"os"

	"github.com/cristianadrielbraun/gofer/internal/models"
	"github.com/cristianadrielbraun/gofer/internal/views"
)

func main() {
	ctx := context.Background()
	accounts := []models.Account{{ID: "acc1", Name: "Ann Example", Email: "ann@example.com"}}
	if err := views.CalendarSidebarBody().Render(ctx, os.Stdout); err != nil {
		log.Fatal(err)
	}
	if err := views.CalendarMainPane(accounts).Render(ctx, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
