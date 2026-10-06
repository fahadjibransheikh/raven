package message

import (
	"bytes"
	"fmt"
	stdhtml "html"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// SanitizeHTML turns untrusted mail HTML into markup that is safe to show in
// the message iframe. It parses the HTML, keeps only an allowlist of layout
// elements and attributes, and neutralises every remote reference: <img> URLs,
// background= attributes and CSS background url()s become markers
// (data-remote-src, data-remote-bg, url("raven-remote:...")) that
// RestoreRemoteImages undoes once the user allows remote content; everything
// else (srcset, other CSS url()s, @import, @font-face, ...) is dropped. Mail without an
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
	var src, marker, bgMarker string
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
		case a.Key == "background" || a.Key == "data-remote-bg":
			if !backgroundElements[el.Data] {
				break
			}
			// A remote background is blocked the way a remote <img> is: the URL moves
			// to a marker that RestoreRemoteImages turns back into background=. The
			// marker is accepted as input so re-sanitizing stored output is stable.
			if v := cleanURLValue(a.Val); a.Key == "background" && isLocalImageURL(v) {
				attrs = append(attrs, html.Attribute{Key: "background", Val: v})
			} else if remote, ok := remoteHTTPURL(v); ok && bgMarker == "" {
				bgMarker = remote
			}
		case allowedAttrs[a.Key] || strings.HasPrefix(a.Key, "aria-"):
			attrs = append(attrs, a)
		}
	}
	if el.Data == "img" {
		attrs = append(imageSourceAttrs(src, marker), attrs...)
	}
	if bgMarker != "" {
		attrs = append(attrs, html.Attribute{Key: "data-remote-bg", Val: bgMarker})
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
	reCSSFetchFn = regexp.MustCompile(`(?i)(?:-[a-z]+-)?(?:image-set|cross-fade|element|image|src|expression)\s*\(`)
	reCSSActive  = regexp.MustCompile(`(?i)(?:-moz-binding|behavior)\s*:`)
	// One declaration or selector per match (strings are consumed whole, so a
	// ; or } inside one does not split it), and the property it starts with.
	reCSSSegment = regexp.MustCompile(`(?s)((?:"[^"]*"|'[^']*'|[^;{}"'])*)(?:[;{}]|$)`)
	reCSSBgProp  = regexp.MustCompile(`(?is)^\s*background(?:-image)?\s*:`)
	reCSSMarker  = regexp.MustCompile(`url\((?:"|&#34;)raven-remote:(.*?)(?:"|&#34;)\)`)
)

// remoteCSSMarker prefixes a remote CSS url() that is blocked for now:
// url("raven-remote:https://..."). It is not a fetchable scheme, so the browser
// ignores it until RestoreRemoteImages strips the prefix.
const remoteCSSMarker = "raven-remote:"

// remoteCSSURL accepts an http(s) URL that can be written back inside url("...").
func remoteCSSURL(v string) (string, bool) {
	if len(v) >= len(remoteCSSMarker) && strings.EqualFold(v[:len(remoteCSSMarker)], remoteCSSMarker) {
		v = v[len(remoteCSSMarker):]
	}
	if strings.ContainsAny(v, "\"'()\\") || strings.Contains(v, "&#") || strings.IndexFunc(v, func(r rune) bool { return r <= ' ' }) >= 0 {
		return "", false
	}
	return remoteHTTPURL(v)
}

// cssCallEnd returns the offset just past the ) closing a call whose ( ends at
// open (or len(d)), so everything nested in image-set(url(a) 1x, url(b) 2x) goes
// with it; strings are skipped so a ) inside one does not close it early.
func cssCallEnd(d string, open int) int {
	depth := 1
	for i := open; i < len(d); i++ {
		switch c := d[i]; c {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return i + 1
			}
		case '"', '\'':
			if end := strings.IndexByte(d[i+1:], c); end >= 0 {
				i += end + 1
			}
		}
	}
	return len(d)
}

// cssBackgroundSpans returns the byte ranges of d that are background or
// background-image declarations: the only place a remote url() is kept. Not
// list-style, content, cursor, border-image, mask, and above all not the src of
// an @font-face.
func cssBackgroundSpans(d string) [][2]int {
	var spans [][2]int
	for _, m := range reCSSSegment.FindAllStringSubmatchIndex(d, -1) {
		if reCSSBgProp.MatchString(d[m[2]:m[3]]) {
			spans = append(spans, [2]int{m[2], m[3]})
		}
	}
	return spans
}

// sanitizeCSS neutralises what CSS can fetch or execute: @import, url() to
// anything but a local image, image-set()/src()/expression() and friends.
// The patterns are matched on a copy with CSS escapes decoded (u\72l( must not
// hide a url()), but only the dangerous spans are rewritten, in the original
// text: harmless escapes such as the .md\:w-1\/2 selectors Tailwind emits stay
// as written. "<" is removed before any pattern runs: stripping it afterwards
// would re-join u<rl( into url( after url() was checked, and it means a <style>
// body can never close its own element. Passes repeat until nothing changes, so
// a rewrite that exposes a new match (comment removal joining tokens) is caught.
func sanitizeCSS(s string) string {
	s = strings.ReplaceAll(s, "<", "")
	for i := 0; i < 8; i++ {
		next := sanitizeCSSPass(s)
		if next == s {
			return s
		}
		s = next
	}
	return "" // never settled: drop it rather than emit something unchecked
}

type cssSpan struct {
	from, to int // byte offsets in the original text
	repl     string
}

func sanitizeCSSPass(s string) string {
	d, pos := cssUnescape(s)
	var spans []cssSpan
	add := func(loc []int, repl string) {
		spans = append(spans, cssSpan{pos[loc[0]], pos[loc[1]], repl})
	}
	for _, m := range reCSSComment.FindAllStringIndex(d, -1) {
		add(m, "")
	}
	if len(spans) == 0 { // comments first: they can hide or join tokens
		for _, m := range reCSSImport.FindAllStringIndex(d, -1) {
			add(m, "")
		}
		bg := cssBackgroundSpans(d)
		for _, m := range reCSSURL.FindAllStringSubmatchIndex(d, -1) {
			v := ""
			for k := 1; k <= 3; k++ {
				if m[2*k] >= 0 {
					v = strings.TrimSpace(d[m[2*k]:m[2*k+1]])
				}
			}
			repl := `url("")`
			if isLocalImageURL(v) && !strings.ContainsAny(v, "\"'()\\ \t\r\n") {
				repl = fmt.Sprintf(`url("%s")`, v)
			} else if remote, ok := remoteCSSURL(v); ok {
				for _, r := range bg {
					if m[0] >= r[0] && m[0] < r[1] {
						repl = fmt.Sprintf(`url("%s%s")`, remoteCSSMarker, remote)
						break
					}
				}
			}
			add(m[:2], repl)
		}
		for _, m := range reCSSFetchFn.FindAllStringIndex(d, -1) {
			add([]int{m[0], cssCallEnd(d, m[1])}, "x-blocked()")
		}
		for _, m := range reCSSActive.FindAllStringIndex(d, -1) {
			add(m, "x-blocked:")
		}
	}
	if len(spans) == 0 {
		return s
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].from < spans[j].from })
	var b strings.Builder
	last := 0
	for _, sp := range spans {
		if sp.from < last { // overlaps a rewrite already made; the next pass revisits it
			continue
		}
		b.WriteString(s[last:sp.from])
		b.WriteString(sp.repl)
		last = sp.to
	}
	b.WriteString(s[last:])
	return b.String()
}

func RestoreRemoteImages(html []byte) []byte {
	s := string(html)

	reImgDouble := regexp.MustCompile(`(?i)(<img\b[^>]*?\s)src\s*=\s*""\s+data-remote-src="([^"]*)"`)
	s = reImgDouble.ReplaceAllString(s, `${1}src="$2"`)

	reImgSingle := regexp.MustCompile(`(?i)(<img\b[^>]*?\s)src\s*=\s*''\s+data-remote-src='([^']*)'`)
	s = reImgSingle.ReplaceAllString(s, `${1}src='$2'`)

	s = rewriteCSSMarkers(s, func(u string) string { return u })
	s = strings.ReplaceAll(s, `data-remote-bg="`, `background="`)
	s = strings.ReplaceAll(s, `url("")`, ``)

	return []byte(s)
}

func IsRemoteImagesBlocked(html string) bool {
	return strings.Contains(html, "data-remote-src") || strings.Contains(html, "data-remote-bg") || strings.Contains(html, remoteCSSMarker)
}

var reExtractRemoteSrcs = regexp.MustCompile(`(?i)(?:data-remote-(?:src|bg)=["']|url\((?:"|&#34;)raven-remote:)((?:[^"'&]|&(?:amp;|[^#"']))*)`)

// ExtractRemoteURLs lists the blocked remote URLs of a stored body: image
// sources, background attributes and CSS url()s. They are HTML-escaped there
// (&amp;), so they are unescaped to the URL that is fetched.
func ExtractRemoteURLs(html string) []string {
	matches := reExtractRemoteSrcs.FindAllStringSubmatch(html, -1)
	seen := make(map[string]bool)
	var urls []string
	for _, m := range matches {
		if u := stdhtml.UnescapeString(m[1]); u != "" && !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	return urls
}

// rewriteCSSMarkers calls f with the URL of each blocked CSS url() and writes
// its result back in the quoting the marker had: &#34; inside an attribute, a
// plain " in a <style> element.
func rewriteCSSMarkers(s string, f func(u string) string) string {
	return reCSSMarker.ReplaceAllStringFunc(s, func(m string) string {
		q := `"`
		if strings.HasPrefix(m, `url(&#34;`) {
			q = `&#34;`
		}
		return "url(" + q + f(reCSSMarker.FindStringSubmatch(m)[1]) + q + ")"
	})
}

// RewriteToLocalAssets points the blocked remote references of a body (images,
// background attributes, CSS url()s) at the downloaded copies, and defuses the
// rest so they are not offered again.
func RewriteToLocalAssets(html []byte, urlToLocal map[string]string) []byte {
	s := string(html)
	for remoteURL, localPath := range urlToLocal {
		for _, u := range []string{remoteURL, stdhtml.EscapeString(remoteURL)} { // as written in an attribute
			s = strings.ReplaceAll(s, `src="" data-remote-src="`+u+`"`, `src="`+localPath+`"`)
			s = strings.ReplaceAll(s, `src='' data-remote-src='`+u+`'`, `src='`+localPath+`'`)
			s = strings.ReplaceAll(s, `data-remote-src="`+u+`"`, `src="`+localPath+`"`)
			s = strings.ReplaceAll(s, `data-remote-src='`+u+`'`, `src='`+localPath+`'`)
			s = strings.ReplaceAll(s, `data-remote-bg="`+u+`"`, `background="`+localPath+`"`)
		}
	}
	s = strings.ReplaceAll(s, `data-remote-src="`, `data-removed-src="`)
	s = strings.ReplaceAll(s, `data-remote-src='`, `data-removed-src='`)
	s = strings.ReplaceAll(s, `data-remote-bg="`, `data-removed-bg="`)
	s = rewriteCSSMarkers(s, func(u string) string {
		if local, ok := urlToLocal[stdhtml.UnescapeString(u)]; ok {
			return local
		}
		return ""
	})
	return []byte(s)
}

// cssUnescape decodes CSS backslash escapes: \ + 1-6 hex digits (and one
// trailing space), \ + newline (a line continuation, dropped), or \ + any
// other character (that character). pos maps every byte of the result (and
// len(result)) to the byte offset in s where the character it came from starts.
func cssUnescape(s string) (string, []int) {
	var b strings.Builder
	pos := make([]int, 0, len(s)+1)
	emit := func(r rune, at int) {
		before := b.Len()
		b.WriteRune(r)
		for n := before; n < b.Len(); n++ {
			pos = append(pos, at)
		}
	}
	for i := 0; i < len(s); {
		start := i
		r, w := utf8.DecodeRuneInString(s[i:])
		i += w
		if r != '\\' || i == len(s) {
			emit(r, start)
			continue
		}
		if isHex(rune(s[i])) {
			n, digits := rune(0), 0
			for i < len(s) && digits < 6 && isHex(rune(s[i])) {
				n = n*16 + rune(hexVal(rune(s[i])))
				i++
				digits++
			}
			if i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '\f') {
				i++
			}
			if n == 0 || n > 0x10FFFF || (n >= 0xD800 && n <= 0xDFFF) {
				n = 0xFFFD
			}
			emit(n, start)
			continue
		}
		r2, w2 := utf8.DecodeRuneInString(s[i:])
		i += w2
		if r2 != '\n' && r2 != '\r' && r2 != '\f' {
			emit(r2, start)
		}
	}
	return b.String(), append(pos, len(s))
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
