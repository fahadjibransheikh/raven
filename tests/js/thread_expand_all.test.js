// "Expand all" in a thread's header opens every collapsed message and then reads "Collapse all".
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

test("expand all opens every message and relabels the button", async () => {
  const msg = function (open) { return '<details data-thread-details' + (open ? " open" : "") + '><summary>s</summary><div class="thread-details-content">body</div></details>' }
  const dom = new JSDOM('<!doctype html><body><div class="mail-view-content"><button data-thread-expand-all><span data-thread-expand-all-label>Expand all</span></button>' + msg(true) + msg(false) + msg(false) + "</div></body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: false, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return Promise.resolve({ ok: true, json: function () { return Promise.resolve({}) } }) }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () { return Promise.resolve() } } }) }
  w.eval(appJS)
  if (w.document.readyState === "loading") await new Promise(function (r) { w.document.addEventListener("DOMContentLoaded", r) })
  const d = w.document
  const label = d.querySelector("[data-thread-expand-all-label]")
  assert.equal(label.textContent, "Expand all")
  d.querySelector("[data-thread-expand-all]").click()
  assert.equal(d.querySelectorAll("details[open]").length, 3)
  assert.equal(label.textContent, "Collapse all")
})
