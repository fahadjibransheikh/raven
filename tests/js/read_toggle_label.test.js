// The read toggle's label names the next action, so it flips with the icon when a message is
// marked read after the toolbar was rendered.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

test("marking read flips the button label and its tooltip", () => {
  const dom = new JSDOM('<!doctype html><body><span data-tui-popover-trigger><button data-read-email="m1" data-is-read="false" aria-label="Mark as read (U)"><svg></svg></button></span><div data-tui-popover-content>Mark as read (U)</div></body>', { runScripts: "outside-only" })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: false, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return new Promise(function () {}) }
  w.eval(appJS)
  const button = w.document.querySelector("button")
  w.setReadButtonIcon(button, true)
  assert.equal(button.getAttribute("aria-label"), "Mark as unread (U)")
  assert.equal(w.document.querySelector("[data-tui-popover-content]").textContent, "Mark as unread (U)")
  w.setReadButtonIcon(button, false)
  assert.equal(button.getAttribute("aria-label"), "Mark as read (U)")
  w.close()
})
