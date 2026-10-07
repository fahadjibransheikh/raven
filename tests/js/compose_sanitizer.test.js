// The compose editor re-sanitizes reply/forward/signature HTML in the browser.
// CSS escapes (u\72l() must not slip a url() past it, and its policy must match
// the server's sanitizeCSS: no remote fetches from the editor.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function loadPage() {
  const dom = new JSDOM("<!doctype html><body></body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.fetch = function () { return new Promise(function () {}) }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  return w
}

const blocked = [
  ["plain remote url", "background: url(https://t.example/p.gif)"],
  ["escaped url(", "background: u\\72l(https://t.example/p.gif)"],
  ["escaped url( with space terminator", "background: \\75 rl(https://t.example/p.gif)"],
  ["fully escaped url(", "background: \\75\\72\\6c(//t.example/p.gif)"],
  ["escaped scheme inside url", "background: url(h\\74tps://t.example/p.gif)"],
  ["comment between tokens", "background: ur/**/l(https://t.example/p.gif)"],
  ["protocol-relative", "background: url(//t.example/p.gif)"],
  ["javascript url", "background: url(javascript:alert(1))"],
  ["escaped javascript", "background: url(\\6a avascript:alert(1))"],
  ["image-set", "background: image-set('https://t.example/p.gif' 1x)"],
  ["escaped image-set", "background: \\69mage-set('https://t.example/p.gif' 1x)"],
  ["raven-remote marker", 'background: url("raven-remote:https://t.example/p.gif")'],
  ["expression", "color: expr\\65ssion(alert(1))"],
  ["import", "width: 1px @import 'https://t.example/x.css'"],
]

test("style sanitizer refuses remote fetches however the CSS is spelled", async function () {
  const w = await loadPage()
  for (const [name, css] of blocked) {
    assert.equal(w._sanitizeComposeStyle(css), "", name + ": " + css)
    const html = w._sanitizeComposeHTML('<div style="' + css.replace(/"/g, "&quot;") + '">x</div>')
    assert.doesNotMatch(html, /t\.example|javascript|expr/i, name + " survived _sanitizeComposeHTML: " + html)
  }
})

test("style sanitizer keeps ordinary and local-image styles", async function () {
  const w = await loadPage()
  assert.equal(w._sanitizeComposeStyle("color: red; font-weight: bold"), "color: red; font-weight: bold")
  assert.equal(w._sanitizeComposeStyle("font-family: \\201CGeorgia\\201D, serif"), "font-family: \\201CGeorgia\\201D, serif")
  assert.equal(w._sanitizeComposeStyle("background: url(cid:logo@x)"), "background: url(cid:logo@x)")
  assert.equal(w._sanitizeComposeStyle('background: url("/api/inline-content/5/logo")'), 'background: url("/api/inline-content/5/logo")')
})

test("escape decoder follows CSS rules", async function () {
  const w = await loadPage()
  assert.equal(w._decodeCSSEscapes("u\\72l"), "url")
  assert.equal(w._decodeCSSEscapes("\\75 rl"), "url")
  assert.equal(w._decodeCSSEscapes("a\\\nb"), "ab")
  assert.equal(w._decodeCSSEscapes("\\0 x"), "\uFFFDx")
  assert.equal(w._decodeCSSEscapes("\\110000"), "\uFFFD")
})

test("a stylesheet's escaped url() does not get inlined into the editor", async function () {
  const w = await loadPage()
  const html = w._sanitizeComposeHTML('<style>.a{background:u\\72l(https://t.example/p.gif)}</style><div class="a">x</div>')
  assert.doesNotMatch(html, /t\.example/)
})
