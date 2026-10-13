package message

import (
	"fmt"
	"strings"
)

// These domains end Message-IDs that Raven invents for mail that arrives
// without one. The IDs are stored, backed by a unique index on
// (account_id, internet_message_id), and regenerated identically on every sync,
// so the values must never change: a different domain would insert duplicates
// of every message already stored with the old one.
const (
	// SyntheticIDDomain ends the IDs made from a folder and UID or a provider ID.
	SyntheticIDDomain = "sync.gofer"
	// LocalIDDomain ends the stand-in thread identity of a message with no ID.
	LocalIDDomain = "gofer.local"
)

// SyntheticID returns the stored Message-ID for the given local part.
func SyntheticID(local string) string {
	return "<" + local + "@" + SyntheticIDDomain + ">"
}

// IsSyntheticID reports whether messageID was invented by SyntheticID.
func IsSyntheticID(messageID string) bool {
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(messageID)), "<>")
	return strings.HasSuffix(normalized, "@"+SyntheticIDDomain)
}

// LocalID returns the thread identity used for message number id.
func LocalID(id int64) string {
	return fmt.Sprintf("local-%d@%s", id, LocalIDDomain)
}
