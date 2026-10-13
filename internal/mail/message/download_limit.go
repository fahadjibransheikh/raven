package message

import (
	"context"
	"fmt"
	"io"
	"os"
)

// AutoDownloadMaxBytes is the largest message Gofer downloads on its own to
// show in the reader. A larger one waits until the reader asks for it, so a
// few huge messages can't fill the server's memory while someone browses.
const AutoDownloadMaxBytes int64 = 25 << 20

// MaxDownloadBytes is the largest message Gofer downloads at all.
const MaxDownloadBytes int64 = 64 << 20

type downloadLimitKey struct{}

// WithDownloadLimit makes message downloads under ctx stop at limit bytes.
func WithDownloadLimit(ctx context.Context, limit int64) context.Context {
	return context.WithValue(ctx, downloadLimitKey{}, limit)
}

// DownloadLimit returns the limit set on ctx when it is lower than max, the
// most the download may ever read.
func DownloadLimit(ctx context.Context, max int64) int64 {
	if limit, ok := ctx.Value(downloadLimitKey{}).(int64); ok && limit > 0 && limit < max {
		return limit
	}
	return max
}

// TooLargeError reports a message larger than the download allowed. Size is
// the message's size when the server said it, or 0.
type TooLargeError struct {
	Size  int64
	Limit int64
}

func (e *TooLargeError) Error() string {
	if e.Size > 0 {
		return fmt.Sprintf("message is %d bytes, more than the %d allowed", e.Size, e.Limit)
	}
	return fmt.Sprintf("message is more than the %d bytes allowed", e.Limit)
}

// CanDownloadFully reports whether the reader can still ask for the whole
// message: it only passed the automatic limit, not the largest Gofer reads.
func (e *TooLargeError) CanDownloadFully() bool {
	return e.Limit < MaxDownloadBytes && e.Size <= MaxDownloadBytes
}

// ReadSavedMessage reads a message saved earlier, within the download limit
// on ctx, or nothing when it can't be read.
func ReadSavedMessage(ctx context.Context, path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer file.Close()
	limit := DownloadLimit(ctx, MaxDownloadBytes)
	if info, err := file.Stat(); err == nil && info.Size() > limit {
		return nil, &TooLargeError{Size: info.Size(), Limit: limit}
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, nil
	}
	return raw, nil
}
