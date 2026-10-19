package handler

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/fahadjibransheikh/raven/internal/models"
	"github.com/fahadjibransheikh/raven/internal/storage"
	"golang.org/x/net/publicsuffix"
)

var displayNameAddress = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)

// senderWarnings runs local look-alike checks on a sender; there is no SPF/DKIM here.
// known maps registrable domains to the messages observed from them.
// shortcut: Reply-To is not stored on messages, so the Reply-To mismatch check is not done; add it with a schema change.
func senderWarnings(fromName, fromEmail string, known map[string]int) []string {
	var warnings []string
	fromEmail = strings.ToLower(strings.TrimSpace(fromEmail))
	if shown := displayNameAddress.FindString(fromName); shown != "" && !strings.EqualFold(shown, fromEmail) {
		warnings = append(warnings, fmt.Sprintf("The sender's name shows a different address (%s) than the one it came from.", shown))
	}
	at := strings.LastIndex(fromEmail, "@")
	if at < 0 {
		return warnings
	}
	host := fromEmail[at+1:]
	if hasLookalikeChars(host) {
		warnings = append(warnings, "This sender's domain uses look-alike characters.")
	}
	registrable := registrableDomain(host)
	if len(registrable) < 5 {
		return warnings
	}
	// A look-alike only counts when the real domain has more history than the sender's own,
	// because the sender's domain is itself observed by the time the message is opened.
	best := ""
	for domain, count := range known {
		if len(domain) < 5 || domain == registrable || count <= known[registrable] || !withinOneEdit(registrable, domain) {
			continue
		}
		if best == "" || count > known[best] || (count == known[best] && domain < best) {
			best = domain
		}
	}
	if best != "" {
		warnings = append(warnings, fmt.Sprintf("This sender's domain looks like %s but isn't.", best))
	}
	return warnings
}

func hasLookalikeChars(host string) bool {
	for _, label := range strings.Split(host, ".") {
		if strings.HasPrefix(label, "xn--") {
			return true
		}
	}
	for _, r := range host {
		if r > 127 {
			return true
		}
	}
	return false
}

func registrableDomain(host string) string {
	if domain, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return domain
	}
	return host
}

// withinOneEdit reports whether a and b differ by exactly one insertion, deletion, substitution or adjacent swap.
func withinOneEdit(a, b string) bool {
	if a == b {
		return false
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) < len(rb) {
		ra, rb = rb, ra
	}
	if len(ra)-len(rb) > 1 {
		return false
	}
	i := 0
	for i < len(rb) && ra[i] == rb[i] {
		i++
	}
	if len(ra) != len(rb) {
		return string(ra[i+1:]) == string(rb[i:])
	}
	if string(ra[i+1:]) == string(rb[i+1:]) {
		return true
	}
	return i+1 < len(ra) && ra[i] == rb[i+1] && ra[i+1] == rb[i] && string(ra[i+2:]) == string(rb[i+2:])
}

// knownSenderDomains folds the user's observed hosts into registrable domains.
// A failed lookup only disables the look-alike check, so it is logged rather than returned.
func knownSenderDomains(ctx context.Context, db *storage.DB, userID string) map[string]int {
	hosts, err := db.ListObservedSenderDomains(ctx, userID)
	if err != nil {
		log.Printf("sender warning: list observed domains: %v", err)
		return nil
	}
	known := make(map[string]int, len(hosts))
	for host, count := range hosts {
		known[registrableDomain(host)] += count
	}
	return known
}

func fillSenderWarnings(ctx context.Context, db *storage.DB, userID string, email *models.Email, thread []models.ThreadItem) {
	known := knownSenderDomains(ctx, db, userID)
	email.SenderWarnings = senderWarnings(email.From.Name, email.From.Email, known)
	for i := range thread {
		thread[i].SenderWarnings = senderWarnings(thread[i].From.Name, thread[i].From.Email, known)
	}
}
