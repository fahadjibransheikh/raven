package handler

import (
	"net/http"
	"sync/atomic"
)

var licenseText atomic.Pointer[string]

// SetLicenseText gives the handler the text of the LICENSE file that main
// embeds in the binary. The MIT License requires its notice, including the
// copyright lines of the code Raven began from, to accompany copies of Raven,
// and a shipped app has no other place to carry it.
func SetLicenseText(text string) { licenseText.Store(&text) }

// serveLicenses serves the license text as plain text; Settings links to it.
func serveLicenses(w http.ResponseWriter, r *http.Request) {
	text := licenseText.Load()
	if text == nil || *text == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(*text))
}
