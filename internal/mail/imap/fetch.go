package imap

import (
	"context"
	"fmt"
	"io"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/fahadjibransheikh/raven/internal/mail/message"
)

func (c *Client) FetchBody(ctx context.Context, folderRemoteName string, uid uint32) ([]byte, error) {
	return c.FetchBodyWithValidity(ctx, folderRemoteName, uid, 0)
}

// FetchBodyWithValidity refuses to fetch a reused UID from a rebuilt mailbox.
func (c *Client) FetchBodyWithValidity(ctx context.Context, folderRemoteName string, uid uint32, expectedValidity uint32) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, fmt.Errorf("client is closed")
	}

	selected, err := c.client.Select(folderRemoteName, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("select %s: %w", folderRemoteName, err)
	}
	defer c.unselect()
	if expectedValidity != 0 && selected.UIDValidity != expectedValidity {
		return nil, fmt.Errorf("IMAP UIDVALIDITY changed during body fetch")
	}

	var uidSet imap.UIDSet
	uidSet.AddNum(imap.UID(uid))

	fetchOpts := &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{
			{Peek: true},
		},
	}

	cmd := c.client.Fetch(uidSet, fetchOpts)
	limit := message.DownloadLimit(ctx, message.MaxDownloadBytes)

	var bodyData []byte

	for {
		msg := cmd.Next()
		if msg == nil {
			break
		}

		for {
			item := msg.Next()
			if item == nil {
				break
			}

			section, ok := item.(imapclient.FetchItemDataBodySection)
			if !ok {
				continue
			}

			// The server announces the size before sending the message, so a
			// message over the limit is refused without reading it. The rest
			// of it is still on its way, so the connection is closed. Closing
			// waits for the message to be consumed, which fails at once when
			// the connection is gone.
			if size := section.Literal.Size(); size > limit {
				c.closed = true
				if c.stopContext != nil {
					c.stopContext()
				}
				closed := make(chan struct{})
				go func() {
					_ = c.client.Close()
					close(closed)
				}()
				_, _ = io.Copy(io.Discard, section.Literal)
				<-closed
				return nil, &message.TooLargeError{Size: size, Limit: limit}
			}
			body, err := io.ReadAll(section.Literal)
			if err != nil {
				return nil, fmt.Errorf("read body literal: %w", err)
			}
			bodyData = body
		}
	}

	if err := cmd.Close(); err != nil {
		return nil, fmt.Errorf("fetch body: %w", err)
	}

	if len(bodyData) == 0 {
		return nil, fmt.Errorf("no body data returned for UID %d", uid)
	}

	return bodyData, nil
}
