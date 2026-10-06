package message

import (
	_ "embed"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// walk visits every node of the parsed output.
func walk(n *html.Node, f func(*html.Node)) {
	f(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, f)
	}
}

func parseOut(t *testing.T, out []byte) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

var forbiddenTags = map[string]bool{
	"script": true, "iframe": true, "object": true, "embed": true, "form": true, "meta": true,
	"link": true, "base": true, "svg": true, "math": true, "input": true, "button": true,
	"noscript": true, "template": true, "video": true, "audio": true, "source": true,
}

// assertInert fails if the sanitized output could execute script or navigate:
// forbidden elements, on* attributes, or a script-capable URL in any attribute.
func assertInert(t *testing.T, in string, out []byte) {
	t.Helper()
	walk(parseOut(t, out), func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		if forbiddenTags[n.Data] {
			t.Errorf("input %q: output still has <%s>: %s", in, n.Data, out)
		}
		for _, a := range n.Attr {
			v := strings.ToLower(strings.Map(func(r rune) rune {
				if r <= ' ' {
					return -1
				}
				return r
			}, a.Val))
			switch {
			case strings.HasPrefix(a.Key, "on"):
				t.Errorf("input %q: event attribute %s survived: %s", in, a.Key, out)
			case strings.Contains(v, "javascript:") || strings.Contains(v, "vbscript:") || strings.HasPrefix(v, "data:text") || strings.HasPrefix(v, "data:application"):
				t.Errorf("input %q: script URL in %s=%q: %s", in, a.Key, a.Val, out)
			case a.Key == "style" && (strings.Contains(v, "expression(") || strings.Contains(v, "behavior:") || strings.Contains(v, "-moz-binding")):
				t.Errorf("input %q: active CSS survived: %s", in, out)
			}
		}
	})
}

func TestSanitizeHTMLRemovesScriptVectors(t *testing.T) {
	inputs := []string{
		`<img/src=x/onerror=alert(1)>`,
		`<img src=x onerror=alert(1)>`,
		`<svg/onload=alert(1)>`,
		`<svg><script>alert(1)</script></svg>`,
		`<math><mtext><img src=x onerror=alert(1)></mtext></math>`,
		`<body onload=alert(1)>hi</body>`,
		`<a href=x onclick=alert(1)>x</a>`,
		`<a href=javascript:alert(1)>x</a>`,
		`<a href=" javascript:alert(1)">x</a>`,
		`<a href="&#106;avascript:alert(1)">x</a>`,
		`<a href="&#x6A;&#x61;vascript&colon;alert(1)">x</a>`,
		`<a href="JaVaScRiPt:alert(1)">x</a>`,
		"<a href=\"java\tscript:alert(1)\">x</a>",
		"<a href=\"java&#9;script:alert(1)\">x</a>",
		"<a href=\"\x01javascript:alert(1)\">x</a>",
		"<a href=\"&#1;javascript:alert(1)\">x</a>",
		`<a href="vbscript:msgbox(1)">x</a>`,
		`<a href="data:text/html,<script>alert(1)</script>">x</a>`,
		`<iframe srcdoc="<script>alert(1)</script>"></iframe>`,
		`<object data="x"></object><embed src="x">`,
		`<form action="https://evil.example"><input name=a><button formaction="javascript:1">x</button></form>`,
		`<base href="https://evil.example/">`,
		`<meta http-equiv="refresh" content="0;url=https://evil.example">`,
		`<noscript><img src=x onerror=alert(1)></noscript>`,
		`<template><img src=x onerror=alert(1)></template>`,
		`<div style="width:expression(alert(1))">x</div>`,
		`<div style="behavior:url(x.htc);-moz-binding:url(x)">x</div>`,
		`<style></sty/**/le><img src=x onerror=alert(1)></style>`,
		`<style>a{}</style ><script>alert(1)</script>`,
		`<scr<script>ipt>alert(1)</scr</script>ipt>`,
		`<img src="x" data-x="1" onerror="alert(1)" ONERROR="alert(2)">`,
		`<input type=image src=x onerror=alert(1)>`,
		`<video poster=x><source src=y onerror=alert(1)></video>`,
		`<!--[if mso]><script>alert(1)</script><![endif]-->`,
		`<a href="//evil.example/x">x</a><img name=body>`,
	}
	for _, in := range inputs {
		out := SanitizeHTML([]byte(in))
		assertInert(t, in, out)
		assertInert(t, in, SanitizeOriginalHTML([]byte(in)))
	}
}

func TestSanitizeHTMLDropsUnsafeLinkTargets(t *testing.T) {
	for _, in := range []string{
		`<a href=javascript:alert(1)>x</a>`,
		`<a href="&#106;avascript:alert(1)">x</a>`,
		"<a href=\"java\tscript:alert(1)\">x</a>",
		`<a href="cid:logo">x</a>`,
		`<a href="/email/1/body">x</a>`,
		`<a href="//evil.example">x</a>`,
	} {
		out := string(SanitizeHTML([]byte(in)))
		if strings.Contains(out, "href") {
			t.Errorf("input %q: href should be dropped, got %s", in, out)
		}
	}
	for _, in := range []string{
		`<a href="https://example.com/?a=1&amp;b=2">x</a>`,
		`<a href="http://example.com">x</a>`,
		`<a href="mailto:a@example.com">x</a>`,
		`<a href="tel:+15551234">x</a>`,
		`<a href="#top">x</a>`,
	} {
		if out := string(SanitizeHTML([]byte(in))); !strings.Contains(out, "href=") {
			t.Errorf("input %q: safe link lost: %s", in, out)
		}
	}
}

// reBlockedCSSURL is the blocked form of a remote CSS background, as written in
// a <style> element or (parsed) in a style attribute.
var reBlockedCSSURL = regexp.MustCompile(`url\("raven-remote:[^"]*"\)`)

// remoteRefs returns every place a remote URL (host t.example) is still
// referenced other than the sanctioned ones: <a href> (a click, not a fetch),
// <img data-remote-src> and background data-remote-bg (the blocked-image
// markers) and url("raven-remote:...") (the blocked-CSS-background marker).
func remoteRefs(t *testing.T, out []byte) []string {
	t.Helper()
	var found []string
	walk(parseOut(t, out), func(n *html.Node) {
		if n.Type == html.TextNode && n.Parent != nil && n.Parent.Data == "style" && strings.Contains(reBlockedCSSURL.ReplaceAllString(n.Data, ""), "t.example") {
			found = append(found, "<style> text: "+n.Data)
		}
		if n.Type != html.ElementNode {
			return
		}
		for _, a := range n.Attr {
			if !strings.Contains(reBlockedCSSURL.ReplaceAllString(a.Val, ""), "t.example") {
				continue
			}
			if (n.Data == "a" && a.Key == "href") || (n.Data == "img" && a.Key == "data-remote-src") || a.Key == "data-remote-bg" {
				continue
			}
			found = append(found, "<"+n.Data+" "+a.Key+"="+a.Val+">")
		}
	})
	return found
}

func TestSanitizeHTMLBlocksEveryRemoteReference(t *testing.T) {
	inputs := []string{
		`<img src="https://t.example/p.gif">`,
		`<img src='https://t.example/p.gif'>`,
		`<img src=https://t.example/p.gif>`,
		`<img SRC = "HTTPS://t.example/p.gif">`,
		`<img src="//t.example/p.gif">`,
		`<img src=//t.example/p.gif>`,
		`<img src="x" srcset="https://t.example/a.png 1x, //t.example/b.png 2x">`,
		`<img srcset="https://t.example/a.png 1x">`,
		`<img src="https://t.example/a" data-remote-src="https://t.example/b">`,
		`<img lowsrc="https://t.example/a" dynsrc="https://t.example/b" longdesc="https://t.example/c">`,
		`<picture><source srcset="https://t.example/a.png"><img src="https://t.example/b.png"></picture>`,
		`<table background="https://t.example/b.png"><tr><td background=//t.example/c.png>x</td></tr></table>`,
		`<body background=//t.example/b.png>x</body>`,
		`<video poster="https://t.example/p.png" src="https://t.example/v.mp4"></video>`,
		`<input type=image src="https://t.example/p.png">`,
		`<svg><image href="https://t.example/p.png"/><image xlink:href="https://t.example/p.png"/></svg>`,
		`<link rel=stylesheet href="https://t.example/x.css">`,
		`<a href="https://example.com" ping="https://t.example/ping">x</a>`,
		`<div style="background:url(https://t.example/p)">x</div>`,
		`<div style="background:url(//t.example/p)">x</div>`,
		`<div style="background: URL( 'https://t.example/p' ) no-repeat">x</div>`,
		`<div style='background:url("//t.example/p")'>x</div>`,
		`<div style="background:u\72l(//t.example/p)">x</div>`,
		`<div style="background:\75rl(//t.example/p)">x</div>`,
		`<div style="background:ur/**/l(//t.example/p)">x</div>`,
		`<div style="background-image:image-set('https://t.example/p.png' 1x)">x</div>`,
		`<div style="background-image:-webkit-image-set(url(//t.example/p.png) 1x)">x</div>`,
		`<div style="content:src('https://t.example/p')">x</div>`,
		`<div style="list-style:url(https://t.example/p)">x</div>`,
		`<style>@import "https://t.example/x.css";</style>`,
		`<style>@import url(//t.example/x.css);</style>`,
		`<style>@IMPORT 'https://t.example/x.css'</style>`,
		`<style>body{background:url(//t.example/p)}</style>`,
		`<style>@font-face{font-family:x;src:url(https://t.example/f.woff)}</style>`,
		`<style>div{background:u\72l(https://t.example/p)}</style>`,
		`<style>@media screen{div{background:url('https://t.example/p')}}</style>`,
	}
	for _, in := range inputs {
		out := SanitizeHTML([]byte(in))
		if refs := remoteRefs(t, out); len(refs) > 0 {
			t.Errorf("input %q: remote refs survive %v in %s", in, refs, out)
		}
	}
}

func TestSanitizeHTMLMarksBlockedImagesForRestore(t *testing.T) {
	for _, in := range []string{
		`<img src="https://t.example/p.gif" width="1">`,
		`<img src=https://t.example/p.gif>`,
		`<img src="//t.example/p.gif">`,
	} {
		out := string(SanitizeHTML([]byte(in)))
		if !strings.Contains(out, `src="" data-remote-src="`) || !strings.Contains(out, `t.example/p.gif"`) {
			t.Errorf("input %q: want blocked marker, got %s", in, out)
		}
		if urls := ExtractRemoteURLs(out); len(urls) != 1 || urls[0] != "https://t.example/p.gif" {
			t.Errorf("input %q: ExtractRemoteURLs = %v", in, urls)
		}
		restored := string(RestoreRemoteImages([]byte(out)))
		if !strings.Contains(restored, `src="https://t.example/p.gif"`) {
			t.Errorf("input %q: restore failed: %s", in, restored)
		}
	}
	// A data-remote-src outside an <img> is not a fetch request.
	if urls := ExtractRemoteURLs(string(SanitizeHTML([]byte(`<div data-remote-src="http://127.0.0.1/x">x</div>`)))); len(urls) != 0 {
		t.Errorf("data-remote-src on a div must not survive: %v", urls)
	}
	// A non-http marker is dropped too.
	if urls := ExtractRemoteURLs(string(SanitizeHTML([]byte(`<img src="" data-remote-src="file:///etc/passwd">`)))); len(urls) != 0 {
		t.Errorf("non-http data-remote-src must not survive: %v", urls)
	}
}

func TestSanitizeHTMLIsIdempotentOnItsOwnOutput(t *testing.T) {
	in := `<p>hi</p><img src="https://t.example/p.gif"><img src="cid:logo"><img src="/api/inline-content/1/x"><img src="/api/remote-assets/1/0123456789abcdef.png">` +
		`<a href="https://example.com">x</a><div style="color:red;background:url(&quot;&quot;)">y</div>`
	once := SanitizeHTML([]byte(in))
	twice := SanitizeHTML(once)
	if string(once) != string(twice) {
		t.Errorf("not idempotent:\n once: %s\ntwice: %s", once, twice)
	}
	for _, want := range []string{`data-remote-src="https://t.example/p.gif"`, `src="cid:logo"`, `src="/api/inline-content/1/x"`, `src="/api/remote-assets/1/0123456789abcdef.png"`} {
		if !strings.Contains(string(once), want) {
			t.Errorf("lost %s in %s", want, once)
		}
	}
}

func TestSanitizeHTMLKeepsEmailLayout(t *testing.T) {
	in := `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>T</title>` +
		`<style type="text/css">.a{color:#c00;font-family:Arial,sans-serif}@media (max-width:600px){.a{width:100%!important}}</style></head>` +
		`<body bgcolor="#eeeeee" style="margin:0"><center><table width="600" cellpadding="0" cellspacing="0" border="0" align="center" style="border-collapse:collapse;background-color:#ffffff">` +
		`<tr><td class="a" valign="top" colspan="2" style="padding:10px 20px;color:#333333"><font color="#ff0000" face="Arial" size="2">Hi</font><br>` +
		`<img src="cid:logo" width="100" height="40" alt="logo" style="display:block"><img src="data:image/png;base64,iVBORw0KGgo="></td></tr></table></center></body></html>`
	out := string(SanitizeHTML([]byte(in)))
	for _, want := range []string{
		"<!DOCTYPE html>", "<html", `lang="en"`, `bgcolor="#eeeeee"`, `style="margin:0"`, "<center>", `width="600"`, `cellpadding="0"`,
		`style="border-collapse:collapse;background-color:#ffffff"`, `valign="top"`, `colspan="2"`, `style="padding:10px 20px;color:#333333"`,
		`<font color="#ff0000" face="Arial" size="2">`, `src="cid:logo"`, `alt="logo"`, `src="data:image/png;base64,iVBORw0KGgo="`,
		"<style", ".a{color:#c00;font-family:Arial,sans-serif}", "@media (max-width:600px){.a{width:100%!important}}", "<title>T</title>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("layout lost %q in %s", want, out)
		}
	}
}

func TestSanitizeHTMLFragmentStaysAFragment(t *testing.T) {
	out := string(SanitizeHTML([]byte(`<p>one</p><table><tr><td>x</td></tr></table>`)))
	if strings.Contains(strings.ToLower(out), "<html") || strings.Contains(strings.ToLower(out), "<body") {
		t.Errorf("fragment gained a document wrapper (buildBodyDocument keys off <html): %s", out)
	}
	if SanitizeHTML(nil) != nil {
		t.Error("empty input must stay nil")
	}
}

func TestRewriteCIDReferencesOnlyTouchesFetchedReferences(t *testing.T) {
	in := `<img src="cid:logo"><a href="cid:logo">x</a><td background="cid:logo"></td><div style="background:url(cid:logo)"></div><p>cid:logo</p>`
	out := string(RewriteCIDReferences([]byte(in), map[string]string{"logo": "/api/inline-content/9/logo"}))
	for _, want := range []string{`<img src="/api/inline-content/9/logo">`, `background="/api/inline-content/9/logo"`, `url(/api/inline-content/9/logo)`, `<a href="cid:logo">`, `<p>cid:logo</p>`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
}

func TestSanitizeCSSKeepsSafeEscapesAsWritten(t *testing.T) {
	for _, in := range []string{
		`<style>.md\:w-1\/2{width:50%}.hover\:bg-blue-500:hover{color:red}.\31 0px{margin:0}</style>`,
		`<style>q:before{content:"\201C"}.sm\:w{color:red}</style>`,
	} {
		out := string(SanitizeHTML([]byte(in)))
		body := strings.TrimSuffix(strings.TrimPrefix(in, `<style>`), `</style>`)
		if !strings.Contains(out, body) {
			t.Errorf("escapes rewritten: in %q out %q", in, out)
		}
	}
}

// unmarked removes the blocked-background markers (url("raven-remote:...")),
// which are inert until the user allows remote content, from rendered output.
func unmarked(out string) string {
	return regexp.MustCompile(`url\((?:"|&#34;)raven-remote:.*?(?:"|&#34;)\)`).ReplaceAllString(out, "")
}

func TestSanitizeCSSStillCatchesEscapedFetches(t *testing.T) {
	for _, in := range []string{
		`<style>.a\:b{background:u\72l(https://evil.example/x.png)}</style>`,
		`<style>.a{background:\75 rl(//evil.example/x.png)}</style>`,
		`<style>@\69mport "https://evil.example/x.css";</style>`,
		`<div style="background:u\72l(//evil.example/x.png)">x</div>`,
		`<div style="b\61ckground:image-s\65t(url(//evil.example/x.png) 1x)">x</div>`,
		`<div style="width:\65xpression(alert(1))">x</div>`,
	} {
		out := string(SanitizeHTML([]byte(in)))
		if strings.Contains(unmarked(out), "evil.example") || strings.Contains(strings.ToLower(out), "expression") {
			t.Errorf("input %q: escaped fetch survived: %s", in, out)
		}
	}
	// A dangerous declaration must not mangle the escapes elsewhere in the sheet.
	out := string(SanitizeHTML([]byte(`<style>.md\:w-1\/2{width:50%}.a{background:url(https://evil.example/x)}</style>`)))
	if !strings.Contains(out, `.md\:w-1\/2{width:50%}`) {
		t.Errorf("safe selector rewritten next to a blocked url: %s", out)
	}
}

//go:embed testdata/nonidempotent_inputs.txt
var nonIdempotentInputs string

// Every input the adversarial run found whose second pass differed from its
// first (68 distinct ones). Sanitizing stored output again (translation does)
// must change nothing, with one exception: markup the x/net parser nests
// differently each time it is re-parsed, e.g. the misnested <a><table><a>.
// That is the parser's round-trip behaviour, not something the sanitizer
// rewrites, and it is stable after the second pass.
func TestSanitizeHTMLIsIdempotentOnAdversarialCorpus(t *testing.T) {
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(nonIdempotentInputs), "\n") {
		in, err := strconv.Unquote(line)
		if err != nil {
			t.Fatalf("bad corpus line %q: %v", line, err)
		}
		n++
		once := SanitizeHTML([]byte(in))
		twice := SanitizeHTML(once)
		if strings.Contains(in, "<a><table><a>") {
			continue
		}
		if string(once) != string(twice) {
			t.Errorf("not idempotent for %q:\n once: %s\ntwice: %s", in, once, twice)
		}
	}
	if n != 68 {
		t.Errorf("corpus has %d inputs, want 68", n)
	}
}

// "<" is stripped before the patterns run; stripping it afterwards re-joined
// u<rl( into url( after url() had been checked.
func TestSanitizeCSSStripsAngleBracketBeforeMatching(t *testing.T) {
	decls := []string{
		`background:u<rl(https://evil.example/x.png)`,
		`background:ur<l(https://evil.example/x.png)`,
		`@font-face{src:u<rl(https://evil.example/f.woff)}`,
		`@im<port 'https://evil.example/x.css';`,
		`background:image-s<et('https://evil.example/x.png' 1x)`,
	}
	var inputs []string
	for _, d := range decls {
		inputs = append(inputs, `<style>*{`+d+`}</style>`, `<div style="`+strings.ReplaceAll(d, `"`, "&quot;")+`">x</div>`)
	}
	inputs = append(inputs,
		`<style>*{background:u<rl(https://evil.example/x.png)}`,
		`<style>@im<port 'https://evil.example/x.css'; *{color:red}</style>`,
		`<style>@font-face src:u<rl(https://evil.example/f.woff)</style>`,
	)
	for _, in := range inputs {
		out := string(SanitizeHTML([]byte(in)))
		// Once "<" is gone u<rl( is a plain background url(), which is only ever
		// emitted as the blocked marker; fonts and @import must vanish entirely.
		if strings.Contains(unmarked(out), "evil.example") || strings.Contains(out, "u<rl") {
			t.Errorf("input %q: remote reference survived: %s", in, out)
		}
		if strings.Contains(in, "@im") || strings.Contains(in, "@font-face") {
			if strings.Contains(out, "evil.example") || strings.Contains(out, "raven-remote") {
				t.Errorf("input %q: import/font reference survived: %s", in, out)
			}
		}
		if again := string(SanitizeHTML([]byte(out))); strings.Contains(unmarked(again), "evil.example") {
			t.Errorf("input %q: output re-joins into a remote reference: %s", in, again)
		}
	}
}

// Remote backgrounds are blocked like remote <img>s: kept as a marker that
// RestoreRemoteImages turns back into the real URL once the user allows remote
// content, and that RewriteToLocalAssets points at the downloaded copy.
func TestRemoteBackgroundsAreMarkedAndRestored(t *testing.T) {
	cases := []struct{ name, in, marker, restored string }{
		{"style attr", `<div style="color:red;background-image:url('https://t.example/h.png?a=1&b=2')">x</div>`,
			`url(&#34;raven-remote:https://t.example/h.png?a=1&amp;b=2&#34;)`, `url(&#34;https://t.example/h.png?a=1&amp;b=2&#34;)`},
		{"style attr shorthand, protocol-relative", `<div style="background:#fff url(//t.example/h.png) no-repeat">x</div>`,
			`url(&#34;raven-remote:https://t.example/h.png&#34;)`, `url(&#34;https://t.example/h.png&#34;)`},
		{"style element", `<style>.hero{background-image:url(https://t.example/h.png)}@media (max-width:480px){.hero{background:url("http://t.example/s.png")}}</style>`,
			`url("raven-remote:https://t.example/h.png")`, `url("https://t.example/h.png")`},
		{"escaped url", `<style>.hero{background:u\72l(https://t.example/h.png)}</style>`,
			`url("raven-remote:https://t.example/h.png")`, `url("https://t.example/h.png")`},
		{"background attribute", `<table><tr><td background="https://t.example/h.png">x</td></tr></table>`,
			`data-remote-bg="https://t.example/h.png"`, `background="https://t.example/h.png"`},
	}
	for _, c := range cases {
		blocked := string(SanitizeHTML([]byte(c.in)))
		if !strings.Contains(blocked, c.marker) {
			t.Errorf("%s: want blocked marker %s in %s", c.name, c.marker, blocked)
		}
		if again := string(SanitizeHTML([]byte(blocked))); again != blocked {
			t.Errorf("%s: marker does not survive re-sanitizing:\n once: %s\ntwice: %s", c.name, blocked, again)
		}
		if !IsRemoteImagesBlocked(blocked) {
			t.Errorf("%s: blocked body not reported as blocked", c.name)
		}
		if urls := ExtractRemoteURLs(blocked); len(urls) == 0 || strings.Contains(urls[0], "&amp;") || !strings.HasPrefix(urls[0], "https://t.example/") {
			t.Errorf("%s: ExtractRemoteURLs = %v", c.name, urls)
		}
		restored := string(RestoreRemoteImages([]byte(blocked)))
		if !strings.Contains(restored, c.restored) || strings.Contains(restored, "raven-remote") || strings.Contains(restored, "data-remote-bg") {
			t.Errorf("%s: want %s restored in %s", c.name, c.restored, restored)
		}
	}
}

func TestRemoteBackgroundsNeverAllowFontsImportsOrOtherFetches(t *testing.T) {
	for _, in := range []string{
		`<style>@font-face{font-family:x;src:url(https://t.example/f.woff2) format("woff2")}</style>`,
		`<style>@font-face{font-family:x;src:url("a;background:"),url(https://t.example/f.woff2)}</style>`,
		`<style>@font-face{font-family:x;src:local(x)}.a{src:url(https://t.example/f2.woff)}</style>`,
		`<style>@import url(https://t.example/x.css);@import "https://t.example/y.css";</style>`,
		`<style>@import 'https://t.example/x.css'; .hero{color:red}</style>`,
		`<div style="list-style:url(https://t.example/p);content:url(https://t.example/p);cursor:url(https://t.example/p),auto;border-image:url(https://t.example/p) 1;mask:url(https://t.example/p)">x</div>`,
		`<div style="background:image-set(url(https://t.example/p) 1x);background-image:-webkit-image-set(url(https://t.example/p) 1x)">x</div>`,
		`<div style="background:cross-fade(url(https://t.example/p), url(https://t.example/q), 50%)">x</div>`,
		`<div style="background:image-set(url(https://t.example/p) 1x, url(https://t.example/q) 2x)">x</div>`,
		`<div style="background:image-set('https://t.example/p)' 1x, url(https://t.example/q) 2x)">x</div>`,
		`<div style="background:url(ftp://t.example/p);background:url(file:///etc/passwd);background:url(javascript:alert(1))">x</div>`,
		`<div style="background:url(raven-remote:file:///etc/passwd);background:url(raven-remote:javascript:alert(1))">x</div>`,
		`<div style="background:url('https://t.example/p&#34;);x:url(&quot;')">x</div>`,
		`<td background="ftp://t.example/p" data-remote-bg="file:///etc/passwd">x</td>`,
		`<div background="https://t.example/p" data-remote-bg="https://t.example/p">x</div>`,
	} {
		out := SanitizeHTML([]byte(in))
		restored := string(RestoreRemoteImages(out))
		if strings.Contains(restored, "t.example") || strings.Contains(restored, "file:") || strings.Contains(restored, "javascript:") || strings.Contains(restored, "ftp:") {
			t.Errorf("input %q: fetchable reference after restore: %s", in, restored)
		}
	}
	// A background next to a blocked font still comes back.
	out := SanitizeHTML([]byte(`<style>@font-face{font-family:x;src:url(https://t.example/f.woff2)}.hero{background:url(https://t.example/h.png)}</style>`))
	restored := string(RestoreRemoteImages(out))
	if !strings.Contains(restored, `url("https://t.example/h.png")`) || strings.Contains(restored, "f.woff2") {
		t.Errorf("hero background lost or font kept: %s", restored)
	}
}

func TestRewriteToLocalAssetsCoversBackgrounds(t *testing.T) {
	in := `<style>.h{background:url(https://t.example/a.png)}.g{background:url(https://t.example/gone.png)}</style>` +
		`<div style="background-image:url(https://t.example/b.png?x=1&y=2)">x</div>` +
		`<table><tr><td background="https://t.example/c.png?x=1&y=2">x</td><td background="https://t.example/gone2.png">y</td></tr></table>` +
		`<img src="https://t.example/d.png?x=1&y=2">`
	body := SanitizeHTML([]byte(in))
	urls := ExtractRemoteURLs(string(body))
	if len(urls) != 6 {
		t.Fatalf("ExtractRemoteURLs = %v, want 6", urls)
	}
	local := map[string]string{
		"https://t.example/a.png":         "/api/remote-assets/1/a.png",
		"https://t.example/b.png?x=1&y=2": "/api/remote-assets/1/b.png",
		"https://t.example/c.png?x=1&y=2": "/api/remote-assets/1/c.png",
		"https://t.example/d.png?x=1&y=2": "/api/remote-assets/1/d.png",
	}
	out := string(RewriteToLocalAssets(body, local))
	for _, want := range []string{`url("/api/remote-assets/1/a.png")`, `url(&#34;/api/remote-assets/1/b.png&#34;)`, `background="/api/remote-assets/1/c.png"`, `src="/api/remote-assets/1/d.png"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
	if strings.Contains(out, "data-remote") || strings.Contains(out, "raven-remote") || strings.Contains(out, `url("https`) {
		t.Errorf("unresolved remote reference left in %s", out)
	}
	// What is left is local, so sanitizing again keeps it and nothing is blocked.
	if again := string(SanitizeHTML([]byte(out))); IsRemoteImagesBlocked(again) || !strings.Contains(again, `/api/remote-assets/1/c.png`) {
		t.Errorf("rewritten body not stable under re-sanitizing: %s", again)
	}
}

// None of these fetches from the network through a url() the sanitizer sees, but
// they have no place in mail either.
func TestSanitizeCSSDropsPaintImageRectAndNamespace(t *testing.T) {
	for _, in := range []string{
		`<div style="background:paint(x)">x</div>`,
		`<div style="background:-moz-image-rect(url(https://t.example/x),0,0,0,0)">x</div>`,
		`<style>@namespace url(https://t.example/);.a{color:red}</style>`,
		`<style>@namespace svg url(http://www.w3.org/2000/svg);</style>`,
	} {
		out := strings.ToLower(string(SanitizeHTML([]byte(in))))
		for _, bad := range []string{"paint(", "image-rect", "@namespace", "t.example", "w3.org"} {
			if strings.Contains(out, bad) {
				t.Errorf("input %q: %s survived: %s", in, bad, out)
			}
		}
	}
}
