//go:build embedded_assets

package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"strings"

	appassets "github.com/cristianadrielbraun/gofer/assets"
)

func assetFileSystem() http.FileSystem {
	return http.FS(appassets.FS)
}

func serveServiceWorker(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, appassets.FS, "js/sw.js")
}

// assetHashes maps each embedded file to its short content hash, computed once.
var assetHashes = func() map[string]string {
	m := map[string]string{}
	_ = fs.WalkDir(appassets.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(appassets.FS, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		m[p] = hex.EncodeToString(sum[:8])
		return nil
	})
	return m
}()

// assetContentVersion is one hash over all embedded assets (fs.WalkDir is sorted).
func assetContentVersion() string {
	h := sha256.New()
	_ = fs.WalkDir(appassets.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			h.Write([]byte(p + assetHashes[p]))
		}
		return err
	})
	return hex.EncodeToString(h.Sum(nil)[:6])
}

// assetETag returns a strong validator; embed.FS has zero mtimes so FileServer sets none.
func assetETag(urlPath string) string {
	if h, ok := assetHashes[strings.TrimPrefix(urlPath, "/")]; ok {
		return `"` + h + `"`
	}
	return ""
}
