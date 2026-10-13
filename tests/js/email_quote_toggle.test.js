// The reader hides quoted history behind a small toggle (script in internal/handler/email_quotes.go,
// injected into every message body frame).
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const go = fs.readFileSync(path.join(__dirname, "..", "..", "internal", "handler", "email_quotes.go"), "utf8")
const script = go.slice(go.indexOf("<script>") + 8, go.indexOf("</script>"))

function load(body) {
  const dom = new JSDOM("<!doctype html><body>" + body + "</body>", { runScripts: "outside-only" })
  dom.window.eval(script)
  return dom.window.document
}

test("a quoted reply is hidden behind a toggle that shows and hides it", () => {
  const d = load('<p>Sounds good, thanks.</p><div class="gmail_quote"><p>On Mon, Ada wrote:</p><blockquote>Can we meet?</blockquote></div>')
  const toggle = d.querySelector("[data-gofer-quote-toggle] button")
  assert.ok(toggle, "toggle is added")
  const quote = d.querySelector(".gmail_quote")
  assert.equal(quote.style.display, "none")
  assert.equal(toggle.getAttribute("aria-expanded"), "false")
  assert.equal(toggle.getAttribute("aria-label"), "Show quoted text")
  toggle.click()
  assert.notEqual(quote.style.display, "none")
  assert.equal(toggle.getAttribute("aria-expanded"), "true")
  assert.equal(toggle.getAttribute("aria-label"), "Hide quoted text")
  toggle.click()
  assert.equal(quote.style.display, "none")
})

test("the toggle follows the text colour with a quiet border", () => {
  // jsdom drops color-mix() when parsing, so check the script's own style string.
  assert.match(script, /border:1px solid color-mix\(in srgb,currentColor 28%,transparent\)/)
  assert.match(script, /color:inherit/)
})

test("a message with nothing above the quote is left alone", () => {
  const d = load('<div class="gmail_quote"><blockquote>old</blockquote></div>')
  assert.equal(d.querySelector("[data-gofer-quote-toggle]"), null)
})
