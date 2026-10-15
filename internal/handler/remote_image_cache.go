package handler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/fahadjibransheikh/raven/internal/mail/message"
	"github.com/fahadjibransheikh/raven/internal/storage"
	"github.com/fahadjibransheikh/raven/internal/store"
)

// Remote images that a reader allowed are relayed through Raven (see
// message_file_links.go). Each relayed image is kept in a "web" folder inside
// the message's remote-assets directory, apart from the images saved by "load
// remote content" (which the stored body refers to and which are never swept),
// under the same name: the file a message already has for an address is the
// one served, so a message opened again, or opened while its images are being
// fetched ahead of time, never downloads them twice. The directory belongs to
// one message, so one message's cache is never read through another's id, and
// it goes wherever the message's other files go. Entries nobody has used for
// relayedImageMaxAge are swept by the retention worker (pruneRelayedImageCache),
// which also holds the cache to relayedImageCacheMaxBytes. Images cached before
// the "web" folder existed sit next to the saved ones and are kept as if saved.

const (
	// maxRelayedFileBytes is the most a relayed file may be; the downloader
	// enforces it too, this keeps the cache honest whatever the downloader is.
	maxRelayedFileBytes = 5 * 1024 * 1024
	warmMaxImages       = 30
	warmConcurrency     = 4
	warmTimeout         = 2 * time.Minute
	downloadTimeout     = 15 * time.Second
	// relayedImageMaxAge and relayedImageCacheMaxBytes bound the cache on disk.
	relayedImageMaxAge        = 14 * 24 * time.Hour
	relayedImageCacheMaxBytes = 1 << 30
)

// warmSlots bounds the images fetched ahead of time across all messages.
var warmSlots = make(chan struct{}, warmConcurrency)

// cachedAssetExtensions are the endings StoreRemoteAsset can give a file.
var cachedAssetExtensions = []string{"", ".png", ".jpg", ".gif", ".webp", ".bmp", ".ico", ".svg"}

type relayFlight struct {
	done chan struct{}
	data []byte
	err  error
}

// withRemoteImageDir runs fn with the directory that holds a message's
// remote images. resolved is false when the owner has no such message (or
// its files can't be reached), in which case fn did not run.
func (h *Handler) withRemoteImageDir(ctx context.Context, owner string, msgID int64, fn func(dir string) error) (resolved bool, err error) {
	if h.userStorage == nil {
		info, err := h.db.GetMessageStorageInfoForUser(ctx, msgID, owner)
		if err != nil || info == nil {
			return false, nil
		}
		return true, fn(h.blobStore.RemoteAssetsDir(info.AccountID, msgID))
	}
	// Managed mode keeps a message's files in the version its body was
	// published in, the same place handleUserRemoteAsset reads from.
	var snapshot *storage.MessageContentSnapshot
	if err := h.withUserDB(ctx, owner, func(db *storage.DB) (err error) {
		snapshot, err = db.GetMessageContentSnapshotForUser(ctx, owner, msgID)
		return err
	}); err != nil || snapshot.BodyHTMLPath == "" {
		return false, nil
	}
	err = h.userStorage.WithAccountActivityForUser(ctx, owner, snapshot.AccountID, func() error {
		resolved = true
		return fn(filepath.Join(filepath.Dir(snapshot.BodyHTMLPath), "remote_assets"))
	})
	return resolved, err
}

// cachedRemoteAsset finds the file a message already has for an address: a saved
// image in dir, or a relayed one in its cache folder.
func cachedRemoteAsset(dir, name string) string {
	for _, where := range []string{dir, store.RelayedImageCacheDir(dir)} {
		for _, ext := range cachedAssetExtensions {
			p := filepath.Join(where, name+ext)
			if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
				if where != dir {
					store.TouchRelayedImage(p, time.Now())
				}
				return p
			}
		}
	}
	return ""
}

// cacheableRelayedImage is true for the files the relay shows as images,
// judged by their bytes: nothing else is kept.
func cacheableRelayedImage(data []byte) bool {
	if len(data) == 0 || len(data) > maxRelayedFileBytes {
		return false
	}
	sniffed := sniffMailFileType(bytes.NewReader(data))
	return mailFileInlineTypes[sniffed] || sniffed == "image/bmp" || sniffed == "image/x-icon" || relayedContentType(data) == "image/svg+xml"
}

func (h *Handler) downloadRemote(ctx context.Context, remoteURL string) ([]byte, error) {
	download := h.remoteResourceDownloader
	if download == nil {
		download = newRemoteResourceDownloader(nil)
	}
	data, err := download(ctx, remoteURL)
	if err == nil && len(data) == 0 {
		err = errors.New("remote file is empty")
	}
	if err == nil && len(data) > maxRelayedFileBytes {
		err = errors.New("remote file is too large")
	}
	return data, err
}

// shareDownload runs work once for everyone asking for key at the same time.
func (h *Handler) shareDownload(ctx context.Context, key string, work func() ([]byte, error)) ([]byte, error) {
	h.relayMu.Lock()
	if flight, ok := h.relayFlights[key]; ok {
		h.relayMu.Unlock()
		select {
		case <-flight.done:
			return flight.data, flight.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	flight := &relayFlight{done: make(chan struct{})}
	if h.relayFlights == nil {
		h.relayFlights = make(map[string]*relayFlight)
	}
	h.relayFlights[key] = flight
	h.relayMu.Unlock()

	flight.data, flight.err = work()

	h.relayMu.Lock()
	delete(h.relayFlights, key)
	h.relayMu.Unlock()
	close(flight.done)
	return flight.data, flight.err
}

// relayedImage returns a remote file of a message: the copy the message
// already has, or a download that is kept when it is an image. The caller has
// verified the signed link. With warm set, nothing is returned for a file that
// is already kept.
func (h *Handler) relayedImage(ctx context.Context, owner string, msgID int64, remoteURL string, warm bool) ([]byte, error) {
	name := store.RemoteAssetName(remoteURL)
	var data []byte
	resolved, err := h.withRemoteImageDir(ctx, owner, msgID, func(dir string) error {
		if p := cachedRemoteAsset(dir, name); p != "" {
			if warm {
				return nil
			}
			var readErr error
			if data, readErr = os.ReadFile(p); readErr == nil {
				return nil
			}
		}
		var err error
		data, err = h.shareDownload(ctx, dir+"|"+name, func() ([]byte, error) {
			// The download outlives the request that started it: others may be
			// waiting for it.
			dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), downloadTimeout)
			defer cancel()
			data, err := h.downloadRemote(dctx, remoteURL)
			if err != nil {
				return nil, err
			}
			if cacheableRelayedImage(data) {
				if _, err := store.WriteRemoteAsset(store.RelayedImageCacheDir(dir), remoteURL, data); err != nil {
					log.Printf("keep relayed image of message %d: %v", msgID, err)
				}
			}
			return data, nil
		})
		return err
	})
	if resolved {
		return data, err
	}
	return h.downloadRemote(ctx, remoteURL)
}

// warmRemoteImages fetches a message's remote images into its cache in the
// background, a few at a time, so opening it finds them ready. urls is called
// in the background and may return nothing. Callers must only warm messages
// whose remote images are allowed: fetching is what tells a sender the
// message was opened.
func (h *Handler) warmRemoteImages(owner string, msgID int64, urls func(context.Context) []string) {
	key := owner + "/" + strconv.FormatInt(msgID, 10)
	if _, busy := h.warming.LoadOrStore(key, struct{}{}); busy {
		return
	}
	go func() {
		defer h.warming.Delete(key)
		ctx, cancel := context.WithTimeout(context.Background(), warmTimeout)
		defer cancel()
		list := urls(ctx)
		if len(list) > warmMaxImages {
			list = list[:warmMaxImages]
		}
		if len(list) == 0 {
			return
		}
		if h.userStorage != nil && h.blobStore != nil {
			release, err := h.blobStore.PinUserFiles(ctx, owner)
			if err != nil {
				return
			}
			defer release()
		}
		var wg sync.WaitGroup
		defer wg.Wait()
		for _, remoteURL := range list {
			select {
			case warmSlots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-warmSlots }()
				_, _ = h.relayedImage(ctx, owner, msgID, remoteURL, true)
			}()
		}
	}()
}

func listedURLs(urls []string) func(context.Context) []string {
	return func(context.Context) []string { return urls }
}

// warmStoredBodyImages warms the images of a message whose body is stored, if
// its remote images are allowed.
func (h *Handler) warmStoredBodyImages(owner string, msgID int64) {
	h.warmRemoteImages(owner, msgID, func(ctx context.Context) []string {
		body := h.allowedStoredBody(ctx, owner, msgID)
		if body == nil {
			return nil
		}
		return message.ExtractRemoteURLs(string(message.SanitizeHTML(body)))
	})
}

// allowedStoredBody returns the stored body of a message, or nil when it has
// none or its remote images are not allowed.
func (h *Handler) allowedStoredBody(ctx context.Context, owner string, msgID int64) []byte {
	id := strconv.FormatInt(msgID, 10)
	if h.userStorage == nil {
		if !h.remoteImagesAllowed(ctx, owner, msgID) {
			return nil
		}
		body, _ := h.db.GetEmailBodyForUser(ctx, id, owner)
		return body
	}
	var body []byte
	_ = h.withUserDB(ctx, owner, func(db *storage.DB) error {
		info, err := db.GetMessageStorageInfoForUser(ctx, msgID, owner)
		if err != nil || info == nil {
			return err
		}
		return h.userStorage.WithAccountForUser(ctx, owner, info.AccountID, func(*storage.DB) error {
			if allowed, err := userRemoteContentAllowed(ctx, db, owner, msgID); err != nil || !allowed {
				return err
			}
			body, err = db.GetEmailBodyForUser(ctx, id, owner)
			return err
		})
	})
	return body
}

// pruneRelayedImageCache keeps the relayed-image cache bounded: it drops entries
// unused for relayedImageMaxAge, those of messages that no longer exist, and the
// least recently used beyond relayedImageCacheMaxBytes. Message existence is
// checked in the single shared database; in managed mode a message lives in its
// owner's database, so only age and size apply there.
func (h *Handler) pruneRelayedImageCache(ctx context.Context, now time.Time) {
	if h.blobStore == nil {
		return
	}
	opts := store.RelayedImageSweep{MaxAge: relayedImageMaxAge, MaxBytes: relayedImageCacheMaxBytes}
	if h.userStorage == nil && h.db != nil {
		opts.MessageExists = func(ctx context.Context, _ string, messageID int64) (bool, error) {
			var one int
			err := h.db.Read().QueryRowContext(ctx, `SELECT 1 FROM messages WHERE id = ?`, messageID).Scan(&one)
			if errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			return err == nil, err
		}
	}
	result, err := h.blobStore.SweepRelayedImages(ctx, now, opts)
	if err != nil && ctx.Err() == nil {
		log.Printf("mail-retention: sweep relayed images: %v", err)
	}
	if result.Files > 0 {
		log.Printf("mail-retention: removed %d cached relayed images (%d bytes)", result.Files, result.Bytes)
	}
}
