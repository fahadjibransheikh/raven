package handler

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Text assets are gzipped once and kept in memory, so each request sends the
// compressed copy without compressing it again. Embedded assets never change,
// so their copy lasts for the life of the process; a file on disk that changes
// (during development) is compressed again on its next request. Fonts and
// images are already compressed and are served as they are.

var compressibleAssetExtensions = map[string]bool{
	".css":         true,
	".js":          true,
	".svg":         true,
	".json":        true,
	".webmanifest": true,
	".md":          true,
	".txt":         true,
}

// Below this size the gzip header outweighs the saving.
const minCompressibleAssetSize = 1024

type compressedAsset struct {
	modTime time.Time
	size    int64
	gzip    []byte
	etag    string
}

type compressedAssetCache struct {
	fs    http.FileSystem
	mu    sync.Mutex
	items map[string]*compressedAsset
}

func newCompressedAssetCache(fs http.FileSystem) *compressedAssetCache {
	return &compressedAssetCache{fs: fs, items: map[string]*compressedAsset{}}
}

// serve writes the gzipped copy of the asset at name and reports whether it
// did. When it returns false the caller serves the file as usual.
func (c *compressedAssetCache) serve(w http.ResponseWriter, r *http.Request, name string) bool {
	if !compressibleAssetExtensions[path.Ext(name)] {
		return false
	}
	// Caches must keep the compressed and plain copies apart.
	w.Header().Add("Vary", "Accept-Encoding")
	if r.Header.Get("Range") != "" || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
		return false
	}
	asset, err := c.get(name)
	if err != nil || asset == nil {
		return false
	}
	// Without a type, ServeContent would sniff the gzipped bytes instead.
	if w.Header().Get("Content-Type") == "" {
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType == "" {
			contentType = "text/plain; charset=utf-8"
		}
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("ETag", asset.etag)
	// ServeContent leaves the length out of encoded responses; range requests
	// never get here, so the whole compressed copy is always what is sent.
	w.Header().Set("Content-Length", strconv.Itoa(len(asset.gzip)))
	http.ServeContent(w, r, name, asset.modTime, bytes.NewReader(asset.gzip))
	return true
}

// get returns the compressed copy of the asset, compressing it if there is no
// copy yet or the file has changed since. It returns nil for a directory or a
// file too small to be worth compressing.
func (c *compressedAssetCache) get(name string) (*compressedAsset, error) {
	name = path.Clean("/" + name)
	file, err := c.fs.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() || info.Size() < minCompressibleAssetSize {
		return nil, nil
	}

	c.mu.Lock()
	cached := c.items[name]
	c.mu.Unlock()
	if cached != nil && cached.size == info.Size() && cached.modTime.Equal(info.ModTime()) {
		return cached, nil
	}

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	asset := &compressedAsset{
		modTime: info.ModTime(),
		size:    info.Size(),
		gzip:    compressed.Bytes(),
		etag:    fmt.Sprintf(`"%x-gzip"`, sum[:12]),
	}

	c.mu.Lock()
	c.items[name] = asset
	c.mu.Unlock()
	return asset, nil
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip, either by
// name or through "*", honouring q=0 as a refusal.
func acceptsGzip(header string) bool {
	gzipQ, starQ := -1.0, -1.0
	for _, part := range strings.Split(header, ",") {
		coding, params, _ := strings.Cut(part, ";")
		q := 1.0
		for _, param := range strings.Split(params, ";") {
			if value, ok := strings.CutPrefix(strings.TrimSpace(param), "q="); ok {
				if parsed, err := strconv.ParseFloat(value, 64); err == nil {
					q = parsed
				}
			}
		}
		switch strings.ToLower(strings.TrimSpace(coding)) {
		case "gzip", "x-gzip":
			gzipQ = q
		case "*":
			starQ = q
		}
	}
	if gzipQ >= 0 {
		return gzipQ > 0
	}
	return starQ > 0
}
