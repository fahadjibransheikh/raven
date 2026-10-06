package message

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// SanitizeHTML turns untrusted mail HTML into markup that is safe to show in
// the message iframe. It parses the HTML, keeps only an allowlist of layout
// elements and attributes, and neutralises every remote reference: <img> URLs
// become a data-remote-src marker (see RestoreRemoteImages), everything else
// (srcset, background, CSS url()/@import, ...) is dropped. Mail without an
// <html> tag stays a fragment because buildBodyDocument keys off that.
func SanitizeHTML(input []byte) []byte {
	return sanitizeHTML(input)
}

// SanitizeOriginalHTML is SanitizeHTML for the "show original" view; the two
// apply the same policy.
func SanitizeOriginalHTML(input []byte) []byte {
	return sanitizeHTML(input)
}

func sanitizeHTML(input []byte) []byte {
	if len(input) == 0 {
		return nil
	}
	var root *html.Node
	if bytes.Contains(bytes.ToLower(input), []byte("<html")) {
		doc, err := html.Parse(bytes.NewReader(input))
		if err != nil {
			return nil
		}
		root = doc
	} else {
		nodes, err := html.ParseFragment(bytes.NewReader(input), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
		if err != nil {
			return nil
		}
		root = &html.Node{Type: html.DocumentNode}
		for _, n := range nodes {
			root.AppendChild(n)
		}
	}
	cleanChildren(root)
	var buf bytes.Buffer
	if err := html.Render(&buf, root); err != nil {
		return nil
	}
	return buf.Bytes()
}

// RewriteCIDReferences points cid: references at the inline-content route. It
// only touches references that are fetched (src=, background=, CSS url()), never
// links, so a message cannot turn a click into a navigation to that route.
func RewriteCIDReferences(html []byte, cidToURL map[string]string) []byte {
	if len(cidToURL) == 0 {
		return html
	}
	s := string(html)
	for cid, target := range cidToURL {
		re := regexp.MustCompile(`(?i)((?:\bsrc|\bbackground)\s*=\s*["']?|url\(\s*["']?)` + regexp.QuoteMeta("cid:"+cid))
		s = re.ReplaceAllString(s, "${1}"+strings.ReplaceAll(target, "$", "$$"))
	}
	return []byte(s)
}

// Elements whose content is meaningless or dangerous without the element.
var dropWithContent = map[string]bool{
	"script": true, "noscript": true, "template": true, "iframe": true, "frame": true, "frameset": true,
	"object": true, "embed": true, "applet": true, "audio": true, "video": true, "canvas": true,
	"select": true, "textarea": true, "xmp": true, "plaintext": true, "noembed": true, "noframes": true,
	"param": true, "source": true, "track": true, "dialog": true, "portal": true, "slot": true,
	"link": true, "meta": true, "base": true, "datalist": true,
}

// Elements kept as-is (attributes are filtered separately). Anything else is
// unwrapped: the element goes, its children stay.
var allowedElements = map[string]bool{}

func init() {
	for _, e := range strings.Fields(`html head body title style a abbr acronym address article aside b bdi bdo big blockquote br
		caption center cite code col colgroup dd del dfn div dl dt em figcaption figure font footer h1 h2 h3 h4 h5 h6 header
		hgroup hr i img ins kbd li main mark nav ol p pre q s samp section small span strike strong sub sup table tbody td
		tfoot th thead time tr tt u ul var wbr`) {
		allowedElements[e] = true
	}
}

// Plain presentational/structural attributes, allowed on every kept element.
var allowedAttrs = map[string]bool{}

func init() {
	for _, a := range strings.Fields(`class id title lang dir align valign bgcolor width height border cellpadding cellspacing
		colspan rowspan nowrap color face size hspace vspace role scope abbr headers span start type value reversed alt
		text link vlink alink marginwidth marginheight leftmargin topmargin frame rules summary datetime bordercolor`) {
		allowedAttrs[a] = true
	}
}

var backgroundElements = map[string]bool{"body": true, "table": true, "tr": true, "td": true, "th": true, "thead": true, "tbody": true, "tfoot": true}

func cleanChildren(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch c.Type {
		case html.ElementNode:
			cleanElement(n, c)
		case html.TextNode:
			if n.Data == "style" && n.Namespace == "" {
				c.Data = sanitizeCSS(c.Data)
			}
		case html.DoctypeNode:
		default: // comments (incl. conditional comments) and anything else
			n.RemoveChild(c)
		}
		c = next
	}
}

func cleanElement(parent, el *html.Node) {
	if el.Namespace != "" || dropWithContent[el.Data] {
		parent.RemoveChild(el)
		return
	}
	cleanChildren(el)
	if !allowedElements[el.Data] {
		for c := el.FirstChild; c != nil; {
			next := c.NextSibling
			el.RemoveChild(c)
			parent.InsertBefore(c, el)
			c = next
		}
		parent.RemoveChild(el)
		return
	}
	cleanAttrs(el)
}

func cleanAttrs(el *html.Node) {
	var attrs []html.Attribute
	var src, marker string
	for _, a := range el.Attr {
		if a.Namespace != "" {
			continue
		}
		switch {
		case a.Key == "style":
			if v := strings.TrimSpace(sanitizeCSS(a.Val)); v != "" {
				attrs = append(attrs, html.Attribute{Key: "style", Val: v})
			}
		case a.Key == "href":
			if el.Data == "a" {
				if v, ok := safeLinkURL(a.Val); ok {
					attrs = append(attrs, html.Attribute{Key: "href", Val: v})
				}
			}
		case a.Key == "src" && el.Data == "img":
			src = cleanURLValue(a.Val)
		case a.Key == "data-remote-src" && el.Data == "img":
			marker = cleanURLValue(a.Val)
		case a.Key == "background":
			if v := cleanURLValue(a.Val); backgroundElements[el.Data] && isLocalImageURL(v) {
				attrs = append(attrs, html.Attribute{Key: "background", Val: v})
			}
		case allowedAttrs[a.Key] || strings.HasPrefix(a.Key, "aria-"):
			attrs = append(attrs, a)
		}
	}
	if el.Data == "img" {
		attrs = append(imageSourceAttrs(src, marker), attrs...)
	}
	el.Attr = attrs
}

// imageSourceAttrs decides an <img>'s src. Local images keep their src. A
// remote one is blocked: src stays empty and the URL moves to data-remote-src,
// directly after it (RestoreRemoteImages and RewriteToLocalAssets match that
// adjacency). The marker is accepted as input too so the output of this
// function is stable when sanitized again (translation re-sanitizes stored
// bodies). Anything else is dropped.
func imageSourceAttrs(src, marker string) []html.Attribute {
	if isLocalImageURL(src) {
		return []html.Attribute{{Key: "src", Val: src}}
	}
	candidate := src
	if candidate == "" {
		candidate = marker
	}
	if remote, ok := remoteHTTPURL(candidate); ok {
		return []html.Attribute{{Key: "src", Val: ""}, {Key: "data-remote-src", Val: remote}}
	}
	return []html.Attribute{{Key: "src", Val: ""}}
}

// cleanURLValue removes what browsers ignore in a URL (tab, CR, LF anywhere;
// control characters and spaces at the ends), so obfuscated schemes are seen as
// the browser would see them.
func cleanURLValue(v string) string {
	v = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, v)
	return strings.TrimFunc(v, func(r rune) bool { return r <= ' ' })
}

var reURLScheme = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*):`)

// safeLinkURL allows only web, mail and phone links plus in-page anchors.
func safeLinkURL(v string) (string, bool) {
	v = cleanURLValue(v)
	if strings.HasPrefix(v, "#") {
		return v, true
	}
	m := reURLScheme.FindStringSubmatch(v)
	if m == nil {
		return "", false
	}
	switch strings.ToLower(m[1]) {
	case "http", "https", "mailto", "tel":
		return v, true
	}
	return "", false
}

// remoteHTTPURL accepts absolute http(s) and protocol-relative URLs.
func remoteHTTPURL(v string) (string, bool) {
	if strings.HasPrefix(v, "//") {
		v = "https:" + v
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return v, true
}

var localImagePrefixes = []string{
	"cid:", "/api/inline-content/", "/api/remote-assets/",
	"data:image/png", "data:image/jpeg", "data:image/jpg", "data:image/gif", "data:image/webp",
	"data:image/bmp", "data:image/svg+xml", "data:image/x-icon",
}

// isLocalImageURL reports whether v can be fetched without leaving the app or
// the message: an inline part, a stored remote asset, or an embedded image.
func isLocalImageURL(v string) bool {
	lower := strings.ToLower(v)
	for _, p := range localImagePrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

var (
	reCSSComment = regexp.MustCompile(`(?s)/\*.*?(?:\*/|$)`)
	reCSSImport  = regexp.MustCompile(`(?is)@import\b[^;{}]*;?`)
	reCSSURL     = regexp.MustCompile(`(?is)url\s*\(\s*(?:"([^"]*)"|'([^']*)'|([^)]*))\s*\)`)
	reCSSFetchFn = regexp.MustCompile(`(?i)(?:-[a-z]+-)?(?:image-set|cross-fade|element|image|src|expression)\s*\([^)]*\)?`)
	reCSSActive  = regexp.MustCompile(`(?i)(?:-moz-binding|behavior)\s*:`)
)

// sanitizeCSS neutralises what CSS can fetch or execute: @import, url() to
// anything but a local image, image-set()/src()/expression() and friends.
// CSS escapes are decoded first (u\72l( must not hide a url()). "<" is removed
// before any pattern runs (and again after decoding, since \3c decodes to it):
// stripping it last would re-join u<rl( into url( after url() was checked. It
// also means a <style> body can never close its own element.
func sanitizeCSS(s string) string {
	s = strings.ReplaceAll(s, "<", "")
	s = strings.ReplaceAll(cssUnescape(s), "<", "")
	s = reCSSComment.ReplaceAllString(s, "")
	s = reCSSImport.ReplaceAllString(s, "")
	s = reCSSURL.ReplaceAllStringFunc(s, func(m string) string {
		sub := reCSSURL.FindStringSubmatch(m)
		v := strings.TrimSpace(sub[1] + sub[2] + sub[3])
		if isLocalImageURL(v) && !strings.ContainsAny(v, "\"'() \t\r\n") {
			return fmt.Sprintf(`url("%s")`, v)
		}
		return `url("")`
	})
	s = reCSSFetchFn.ReplaceAllString(s, "x-blocked()")
	return reCSSActive.ReplaceAllString(s, "x-blocked:")
}

func RestoreRemoteImages(html []byte) []byte {
	s := string(html)

	reImgDouble := regexp.MustCompile(`(?i)(<img\b[^>]*?\s)src\s*=\s*""\s+data-remote-src="([^"]*)"`)
	s = reImgDouble.ReplaceAllString(s, `${1}src="$2"`)

	reImgSingle := regexp.MustCompile(`(?i)(<img\b[^>]*?\s)src\s*=\s*''\s+data-remote-src='([^']*)'`)
	s = reImgSingle.ReplaceAllString(s, `${1}src='$2'`)

	s = strings.ReplaceAll(s, `url("")`, ``)

	return []byte(s)
}

func IsRemoteImagesBlocked(html string) bool {
	return strings.Contains(html, "data-remote-src")
}

var reExtractRemoteSrcs = regexp.MustCompile(`(?i)data-remote-src=["']([^"']+)["']`)

func ExtractRemoteURLs(html string) []string {
	matches := reExtractRemoteSrcs.FindAllStringSubmatch(html, -1)
	seen := make(map[string]bool)
	var urls []string
	for _, m := range matches {
		if len(m) > 1 && !seen[m[1]] {
			seen[m[1]] = true
			urls = append(urls, m[1])
		}
	}
	return urls
}

func RewriteToLocalAssets(html []byte, urlToLocal map[string]string) []byte {
	s := string(html)
	for remoteURL, localPath := range urlToLocal {
		s = strings.ReplaceAll(s, `src="" data-remote-src="`+remoteURL+`"`, `src="`+localPath+`"`)
		s = strings.ReplaceAll(s, `src='' data-remote-src='`+remoteURL+`'`, `src='`+localPath+`'`)
		s = strings.ReplaceAll(s, `data-remote-src="`+remoteURL+`"`, `src="`+localPath+`"`)
		s = strings.ReplaceAll(s, `data-remote-src='`+remoteURL+`'`, `src='`+localPath+`'`)
	}
	s = strings.ReplaceAll(s, `data-remote-src="`, `data-removed-src="`)
	s = strings.ReplaceAll(s, `data-remote-src='`, `data-removed-src='`)
	return []byte(s)
}

// cssUnescape decodes CSS backslash escapes: \ + 1-6 hex digits (and one
// trailing space), \ + newline (a line continuation, dropped), or \ + any
// other character (that character).
func cssUnescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		if r[i] != '\\' || i+1 == len(r) {
			b.WriteRune(r[i])
			continue
		}
		i++
		if isHex(r[i]) {
			n, digits := rune(0), 0
			for i < len(r) && digits < 6 && isHex(r[i]) {
				n = n*16 + rune(hexVal(r[i]))
				i++
				digits++
			}
			if i < len(r) && (r[i] == ' ' || r[i] == '\t' || r[i] == '\n' || r[i] == '\r' || r[i] == '\f') {
				i++
			}
			i--
			if n == 0 || n > 0x10FFFF || (n >= 0xD800 && n <= 0xDFFF) {
				n = 0xFFFD
			}
			b.WriteRune(n)
		} else if r[i] != '\n' && r[i] != '\r' && r[i] != '\f' {
			b.WriteRune(r[i])
		}
	}
	return b.String()
}

func isHex(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func hexVal(r rune) int {
	switch {
	case r >= 'a':
		return int(r-'a') + 10
	case r >= 'A':
		return int(r-'A') + 10
	}
	return int(r - '0')
}
