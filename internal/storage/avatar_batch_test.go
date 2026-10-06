package storage

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/cristianadrielbraun/gofer/internal/models"
)

// scanEmailRows hydrates sender avatars with the batch helper; it must give the
// same result as the per-contact helper it replaced for every avatar state.
func TestBatchAvatarHydrationMatchesPerContact(t *testing.T) {
	ctx := context.Background()
	db := newContactsTestDB(t)
	future, past := time.Now().Add(time.Hour), time.Now().Add(-time.Hour)
	senders := map[string]func(hash, email string) error{
		"found@example.com": func(h, e string) error {
			return db.SaveSenderAvatarFound(ctx, h, e, "gravatar", "image/png", "", []byte{1, 2}, future, "found", "")
		},
		"expired@example.com": func(h, e string) error {
			return db.SaveSenderAvatarFound(ctx, h, e, "bimi", "image/png", "", []byte{1}, past, "found", "")
		},
		"missing@example.com": func(h, e string) error { return db.SaveSenderAvatarMissing(ctx, h, e, "gravatar", future, "missing", "") },
		"error@example.com":   func(h, e string) error { return db.SaveSenderAvatarError(ctx, h, e, "x", "boom", future, "error", "") },
	}
	var contacts []models.Contact
	for email, save := range senders {
		c := contactFromSender("", email)
		if err := save(c.AvatarHash, email); err != nil {
			t.Fatal(err)
		}
		contacts = append(contacts, c)
	}
	contacts = append(contacts, contactFromSender("Nobody", "unseen@example.com"), contactFromSender("", "found@example.com"))

	single := append([]models.Contact(nil), contacts...)
	for i := range single {
		db.hydrateContactAvatar(ctx, &single[i])
	}
	batch := append([]models.Contact(nil), contacts...)
	db.hydrateContactAvatars(ctx, batch)
	if !reflect.DeepEqual(single, batch) {
		t.Fatalf("batch hydration differs from per-contact:\nsingle=%+v\nbatch =%+v", single, batch)
	}
	if single[0].AvatarStatus == "" {
		t.Fatal("test did not exercise any avatar state")
	}
}
