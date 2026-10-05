package message

import (
	"io"
	"strings"

	"github.com/emersion/go-message/mail"

	"github.com/cristianadrielbraun/gofer/internal/mail/ical"
)

// ExtractCalendar returns the first iCalendar payload in a raw RFC 822 message:
// a text/calendar part (with or without method=) or an .ics attachment. At
// most ical.MaxBytes are read from the part; other parts are skipped unread.
func ExtractCalendar(r io.Reader) []byte {
	mr, err := mail.CreateReader(r)
	if err != nil {
		return nil
	}
	for i := 0; i < 200; i++ { // bound the part walk on hostile messages
		part, err := mr.NextPart()
		if err != nil {
			return nil
		}
		var ct, name string
		switch h := part.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ = h.ContentType()
		case *mail.AttachmentHeader:
			ct, _, _ = h.ContentType()
			name, _ = h.Filename()
		default:
			continue
		}
		ct = strings.ToLower(ct)
		if ct != "text/calendar" && ct != "application/ics" && !strings.HasSuffix(strings.ToLower(name), ".ics") {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part.Body, ical.MaxBytes))
		if err != nil || len(data) == 0 {
			continue
		}
		return data
	}
	return nil
}
