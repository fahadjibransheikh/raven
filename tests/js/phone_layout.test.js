// Runs assets/js/phone-layout.js in jsdom against a minimal mail shell.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const src = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "phone-layout.js"), "utf8")
const EMPTY = '<div data-mail-view-empty>Select an email</div>'

function load(body) {
  const dom = new JSDOM("<!doctype html><body>" + body + "</body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.eval(src)
  return w
}

// Two frames: MutationObserver microtask, then the rAF debounce.
function settle(w) {
  return new Promise(function (resolve) { w.requestAnimationFrame(function () { w.requestAnimationFrame(resolve) }) })
}

const pane = function (w) { return w.document.documentElement.getAttribute("data-phone-pane") }
const shell = '<div id="app-shell"><div id="mail-list"></div><div id="mail-view">' + EMPTY + "</div></div>"

test("sets data-phone when the query matches", () => {
  assert.equal(load(shell).document.documentElement.hasAttribute("data-phone"), true)
})

test("pane follows #mail-view content and replacement", async () => {
  const w = load(shell)
  const doc = w.document
  assert.equal(pane(w), "list")

  doc.getElementById("mail-view").innerHTML = "<article>message</article>"
  await settle(w)
  assert.equal(pane(w), "view")

  doc.getElementById("mail-view").outerHTML = '<div id="mail-view">' + EMPTY + "</div>"
  await settle(w)
  assert.equal(pane(w), "list")

  doc.getElementById("mail-view").outerHTML = '<div id="mail-view"><article>other</article></div>'
  await settle(w)
  assert.equal(pane(w), "view")

  doc.getElementById("mail-view").innerHTML = EMPTY
  await settle(w)
  assert.equal(pane(w), "list")
})

test("no pane on the contacts page", () => {
  const w = load('<div id="app-shell"><div id="contacts-list-scroll"></div><section id="mail-view"><p>x</p></section></div>')
  assert.equal(pane(w), null)
})

test("no-op without #app-shell", () => {
  assert.doesNotThrow(() => load("<p>login</p>"))
  assert.equal(pane(load("<p>login</p>")), null)
})

test("drawer toggles via hamburger, closes on backdrop, Escape and sidebar link click", () => {
  const w = load('<div id="app-shell"><aside data-app-sidebar><a href="#x">Inbox</a></aside><button data-phone-nav-toggle aria-expanded="false"></button><div id="mail-list"></div><div id="mail-view"><div data-mail-view-empty></div></div></div>')
  const doc = w.document
  const nav = () => doc.documentElement.getAttribute("data-phone-nav")
  const toggle = doc.querySelector("[data-phone-nav-toggle]")

  toggle.click()
  assert.equal(nav(), "open")
  assert.equal(toggle.getAttribute("aria-expanded"), "true")

  doc.getElementById("phone-nav-backdrop").click()
  assert.equal(nav(), null)
  assert.equal(toggle.getAttribute("aria-expanded"), "false")

  toggle.click()
  doc.dispatchEvent(new w.KeyboardEvent("keydown", { key: "Escape", bubbles: true }))
  assert.equal(nav(), null)

  toggle.click()
  doc.querySelector("aside a").click()
  assert.equal(nav(), null)
})
