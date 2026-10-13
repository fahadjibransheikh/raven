package store

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Remote images relayed through Raven are kept so a message opened again does
// not download them twice, but they are a cache, not part of the message: they
// live in a "web" folder inside the message's remote_assets directory, apart
// from the images the reader chose to save (those are referenced by the stored
// body and are never swept).
const (
	relayedCacheDirName = "web"
	remoteAssetsDirName = "remote_assets"
	// staleTempAge is how long a half-written temporary file may sit before it
	// is considered abandoned.
	staleTempAge = time.Hour
)

// RelayedImageCacheDir is where the relayed images of a message are kept, given
// the message's remote_assets directory.
func RelayedImageCacheDir(remoteAssetsDir string) string {
	return filepath.Join(remoteAssetsDir, relayedCacheDirName)
}

// RelayedImageSweep says what to keep in the relayed-image cache.
type RelayedImageSweep struct {
	// MaxAge removes entries not read or written for this long. Zero keeps all.
	MaxAge time.Duration
	// MaxBytes evicts the least recently used entries beyond this total. Zero
	// means no cap.
	MaxBytes int64
	// MessageExists, if set, is asked about each message that has cache entries;
	// entries of a message that no longer exists are removed.
	MessageExists func(ctx context.Context, accountID string, messageID int64) (bool, error)
}

// RelayedImageSweepResult counts what a sweep removed.
type RelayedImageSweepResult struct {
	Files int
	Bytes int64
}

type relayedCacheEntry struct {
	path    string
	modTime time.Time
	size    int64
}

// SweepRelayedImages deletes expired, orphaned and over-cap entries of the
// relayed-image cache under the blob directory. It only ever looks inside
// remote_assets/web folders, so saved remote images, bodies and attachments are
// out of its reach. A reader refreshes an entry's modification time when it
// uses it, so "old" means unused.
func (s *BlobStore) SweepRelayedImages(ctx context.Context, now time.Time, opts RelayedImageSweep) (RelayedImageSweepResult, error) {
	var result RelayedImageSweepResult
	var kept []relayedCacheEntry
	remove := func(e relayedCacheEntry) {
		if err := os.Remove(e.path); err == nil || errors.Is(err, fs.ErrNotExist) {
			result.Files++
			result.Bytes += e.size
		}
	}
	accounts, err := os.ReadDir(s.basePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return result, nil
		}
		return result, err
	}
	exists := map[string]bool{}
	for _, account := range accounts {
		if !account.IsDir() || account.Name() == "avatars" || strings.HasPrefix(account.Name(), "_") {
			continue
		}
		messages := filepath.Join(s.basePath, account.Name(), "messages")
		walkErr := filepath.WalkDir(messages, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			if !d.IsDir() {
				return nil
			}
			if d.Name() == "attachments" {
				return fs.SkipDir
			}
			if d.Name() != relayedCacheDirName || filepath.Base(filepath.Dir(path)) != remoteAssetsDirName {
				return nil
			}
			orphan := false
			if opts.MessageExists != nil {
				if id, ok := messageIDFromPath(messages, path); ok {
					key := account.Name() + "/" + strconv.FormatInt(id, 10)
					present, known := exists[key]
					if !known {
						var qerr error
						present, qerr = opts.MessageExists(ctx, account.Name(), id)
						if qerr != nil {
							return qerr
						}
						exists[key] = present
					}
					orphan = !present
				}
			}
			files, _ := os.ReadDir(path)
			for _, f := range files {
				info, ierr := f.Info()
				if ierr != nil || !info.Mode().IsRegular() {
					continue
				}
				entry := relayedCacheEntry{path: filepath.Join(path, f.Name()), modTime: info.ModTime(), size: info.Size()}
				switch {
				case orphan:
					remove(entry)
				case strings.HasPrefix(f.Name(), ".tmp-"):
					if now.Sub(entry.modTime) > staleTempAge {
						remove(entry)
					}
				case opts.MaxAge > 0 && now.Sub(entry.modTime) > opts.MaxAge:
					remove(entry)
				default:
					kept = append(kept, entry)
				}
			}
			return fs.SkipDir
		})
		if walkErr != nil {
			return result, walkErr
		}
	}
	if opts.MaxBytes > 0 {
		var total int64
		for _, e := range kept {
			total += e.size
		}
		sort.Slice(kept, func(i, j int) bool { return kept[i].modTime.Before(kept[j].modTime) })
		for _, e := range kept {
			if total <= opts.MaxBytes {
				break
			}
			remove(e)
			total -= e.size
		}
	}
	return result, nil
}

// messageIDFromPath reads the message id out of <messages>/<id>/....
func messageIDFromPath(messages, path string) (int64, bool) {
	rel, err := filepath.Rel(messages, path)
	if err != nil {
		return 0, false
	}
	first, _, _ := strings.Cut(rel, string(filepath.Separator))
	id, err := strconv.ParseInt(first, 10, 64)
	return id, err == nil && id > 0
}

// TouchRelayedImage marks a cached image as used just now, so the sweep keeps
// what readers still use. It only writes when the mark is more than an hour old.
func TouchRelayedImage(path string, now time.Time) {
	if info, err := os.Stat(path); err == nil && now.Sub(info.ModTime()) > time.Hour {
		_ = os.Chtimes(path, now, now)
	}
}
