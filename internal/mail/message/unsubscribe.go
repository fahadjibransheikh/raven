package message

import (
	"bufio"
	"bytes"
	"net/mail"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
)

var reAngleURI = regexp.MustCompile(`<([^<>]*)>`)

// Unsubscribe is what a message's List-Unsubscribe headers (RFC 2369) offer.
type Unsubscribe struct {
	HTTPS    string // first https: URI
	Mailto   string // first mailto: URI
	OneClick bool   // RFC 8058: HTTPS present and List-Unsubscribe-Post is List-Unsubscribe=One-Click
}

// Unsubscribe methods in priority order, as returned by Method.
const (
	UnsubscribeOneClick = "one-click"
	UnsubscribeMailto   = "mailto"
	UnsubscribeBrowser  = "browser"
)

// ParseListUnsubscribe reads the two raw header values. Only <angle-bracketed> URIs
// count; http: URIs are ignored (no one-click over plaintext).
func ParseListUnsubscribe(header, post string) Unsubscribe {
	var u Unsubscribe
	for _, m := range reAngleURI.FindAllStringSubmatch(header, -1) {
		raw := strings.Join(strings.Fields(m[1]), "") // folding can leave whitespace inside
		parsed, err := url.Parse(raw)
		if err != nil {
			continue
		}
		switch strings.ToLower(parsed.Scheme) {
		case "https":
			if u.HTTPS == "" && parsed.Host != "" {
				u.HTTPS = raw
			}
		case "mailto":
			if u.Mailto == "" && parsed.Opaque != "" {
				u.Mailto = raw
			}
		}
	}
	u.OneClick = u.HTTPS != "" && strings.TrimSpace(post) == "List-Unsubscribe=One-Click"
	return u
}

// CaptureListUnsubscribe applies the storage rule to raw header values: an oversized
// List-Unsubscribe is dropped together with its List-Unsubscribe-Post companion.
func CaptureListUnsubscribe(header, post string) (string, string) {
	if header == "" || len(header) > 4096 {
		return "", ""
	}
	return header, post
}

// ParseListUnsubscribeHeaders extracts the raw List-Unsubscribe pair from a header block.
func ParseListUnsubscribeHeaders(raw []byte) (header, post string) {
	h, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(raw))).ReadMIMEHeader()
	if err != nil && len(h) == 0 {
		return "", ""
	}
	return CaptureListUnsubscribe(h.Get("List-Unsubscribe"), h.Get("List-Unsubscribe-Post"))
}

// Method is the best available way to unsubscribe, or "" when there is none.
func (u Unsubscribe) Method() string {
	switch {
	case u.OneClick:
		return UnsubscribeOneClick
	case u.Mailto != "":
		return UnsubscribeMailto
	case u.HTTPS != "":
		return UnsubscribeBrowser
	}
	return ""
}

// MailtoRequest splits a mailto: URI into recipient, subject and body. The subject
// defaults to "unsubscribe". ok is false when no valid address is present.
func MailtoRequest(uri string) (to *mail.Address, subject, body string, ok bool) {
	parsed, err := url.Parse(uri)
	if err != nil || !strings.EqualFold(parsed.Scheme, "mailto") {
		return nil, "", "", false
	}
	addr, err := url.PathUnescape(parsed.Opaque)
	if err != nil {
		return nil, "", "", false
	}
	list, err := mail.ParseAddressList(addr)
	if err != nil || len(list) == 0 {
		return nil, "", "", false
	}
	q := parsed.Query()
	subject = strings.Join(strings.Fields(q.Get("subject")), " ") // no header injection via newlines
	if subject == "" {
		subject = "unsubscribe"
	}
	return list[0], subject, q.Get("body"), true
}
