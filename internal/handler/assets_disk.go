//go:build !embedded_assets

package handler

import "net/http"

func assetFileSystem() http.FileSystem {
	return http.Dir("./assets")
}

func serveServiceWorker(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "./assets/js/sw.js")
}

// Disk builds are development builds: no-store, so no versioning or validators.
func assetContentVersion() string { return "dev" }
func assetETag(string) string     { return "" }
