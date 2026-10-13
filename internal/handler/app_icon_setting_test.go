package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func patchUISettings(h *Handler, body string) *httptest.ResponseRecorder {
	req := ownerRequest(httptest.NewRequest(http.MethodPatch, "/api/settings/ui", strings.NewReader(body)))
	rec := httptest.NewRecorder()
	h.handleSaveUISettings(rec, req)
	return rec
}

func TestAppIconSettingDefaultsToRavenAndRejectsUnknownValues(t *testing.T) {
	h, db := newAccountOwnershipTestHandler(t)

	if got := db.GetUISettings(t.Context(), "owner")["app_icon"]; got != "raven" {
		t.Fatalf("default app_icon = %q, want raven", got)
	}

	for _, style := range []string{"raven", "classic", "minimal"} {
		if rec := patchUISettings(h, `{"app_icon":"`+style+`"}`); rec.Code != http.StatusOK {
			t.Fatalf("PATCH app_icon=%s status = %d body = %q", style, rec.Code, rec.Body.String())
		}
		if got := db.GetUISettings(t.Context(), "owner")["app_icon"]; got != style {
			t.Fatalf("stored app_icon = %q, want %q", got, style)
		}
	}

	for _, bad := range []string{"", "Raven", "express", "../logo", "raven-light"} {
		rec := patchUISettings(h, `{"theme":"light","app_icon":"`+bad+`"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH app_icon=%q status = %d, want 400", bad, rec.Code)
		}
	}
	settings := db.GetUISettings(t.Context(), "owner")
	if settings["app_icon"] != "minimal" {
		t.Errorf("a rejected PATCH changed app_icon to %q", settings["app_icon"])
	}
	if settings["theme"] == "light" {
		t.Error("a rejected PATCH still applied its other keys")
	}

	// Requests that do not mention app_icon are unaffected.
	if rec := patchUISettings(h, `{"theme":"light"}`); rec.Code != http.StatusOK {
		t.Errorf("PATCH without app_icon status = %d", rec.Code)
	}
}
