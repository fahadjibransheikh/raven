package imap

import (
	"context"
	"fmt"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// FetchSizes returns the sizes the server reports for the given UIDs. UIDs
// the server no longer has are left out.
func (c *Client) FetchSizes(ctx context.Context, remoteName string, uids []uint32, expectedUIDValidity uint32) (map[uint32]int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.closed {
		return nil, fmt.Errorf("client is closed")
	}
	if len(uids) == 0 {
		return nil, nil
	}
	selected, err := c.client.Select(remoteName, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("select %s: %w", remoteName, err)
	}
	defer c.unselect()
	if uidValidityChanged(expectedUIDValidity, uint32(selected.UIDValidity)) {
		return nil, fmt.Errorf("IMAP UIDVALIDITY changed during size fetch")
	}

	sizes := make(map[uint32]int64, len(uids))
	const chunkSize = 500
	for start := 0; start < len(uids); start += chunkSize {
		var uidSet imap.UIDSet
		for _, uid := range uids[start:min(start+chunkSize, len(uids))] {
			uidSet.AddNum(imap.UID(uid))
		}
		cmd := c.client.Fetch(uidSet, &imap.FetchOptions{UID: true, RFC822Size: true})
		for {
			msg := cmd.Next()
			if msg == nil {
				break
			}
			var uid uint32
			var size int64
			for {
				item := msg.Next()
				if item == nil {
					break
				}
				switch item := item.(type) {
				case imapclient.FetchItemDataUID:
					uid = uint32(item.UID)
				case imapclient.FetchItemDataRFC822Size:
					size = item.Size
				}
			}
			if uid > 0 && size > 0 {
				sizes[uid] = size
			}
		}
		if err := cmd.Close(); err != nil {
			return nil, fmt.Errorf("fetch sizes %s: %w", remoteName, err)
		}
	}
	return sizes, nil
}
