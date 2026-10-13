package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeLicensesReturnsThePlainTextLicense(t *testing.T) {
	SetLicenseText("MIT License\n\nCopyright (c) Someone\n")
	t.Cleanup(func() { SetLicenseText("") })
	mux := http.NewServeMux()
	setupAssetsRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/licenses", nil))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") || !strings.Contains(rec.Body.String(), "Copyright (c) Someone") {
		t.Fatalf("status %d, type %q, body %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}

func TestServeLicensesIsNotFoundWhenNothingWasEmbedded(t *testing.T) {
	SetLicenseText("")
	rec := httptest.NewRecorder()
	serveLicenses(rec, httptest.NewRequest(http.MethodGet, "/licenses", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}
