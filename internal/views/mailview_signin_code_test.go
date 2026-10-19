package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fahadjibransheikh/raven/internal/models"
)

func renderMailViewContent(t *testing.T, email *models.Email, thread []models.ThreadItem) string {
	t.Helper()
	var buf bytes.Buffer
	if err := MailViewContent(email, thread, true).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestMailViewShowsCopyChipForSignInCode(t *testing.T) {
	email := &models.Email{ID: "m1", Subject: "Your verification code", TextBody: "Your verification code is 482913. It expires in 10 minutes."}
	for name, thread := range map[string][]models.ThreadItem{
		"single": nil,
		"thread": {{ID: "m0"}, {ID: "m1"}},
	} {
		html := renderMailViewContent(t, email, thread)
		if !strings.Contains(html, `id="signin-code-m1"`) || !strings.Contains(html, ">482913<") {
			t.Errorf("%s view: sign-in code chip missing", name)
		}
		if !strings.Contains(html, `data-target-id="signin-code-m1"`) {
			t.Errorf("%s view: chip has no copy button for the code", name)
		}
	}
}

func TestMailViewOmitsCopyChipWithoutSignInCode(t *testing.T) {
	email := &models.Email{ID: "m1", Subject: "Order 482913 shipped", TextBody: "Your order 482913 has shipped."}
	if html := renderMailViewContent(t, email, nil); strings.Contains(html, "signin-code") {
		t.Fatal("chip rendered for a message without a sign-in code")
	}
}
