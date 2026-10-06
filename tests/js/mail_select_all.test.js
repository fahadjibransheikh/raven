// The select-all checkbox selects every rendered row and is tri-state.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

test("select-all toggles every rendered row and reports indeterminate", async () => {
  const rows = [1, 2, 3].map(function (n) { return '<div class="mail-list-item" data-email-id="m' + n + '"><a href="#"></a></div>' }).join("")
  const dom = new JSDOM('<!doctype html><body><input type="checkbox" data-mail-select-all><div id="mail-list-scroll">' + rows + "</div></body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return Promise.resolve({ ok: true, json: function () { return Promise.resolve({}) } }) }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () {} } }) }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  const box = w.document.querySelector("[data-mail-select-all]")
  const selected = function () { return w.document.querySelectorAll(".mail-list-item[data-mail-selected]").length }

  box.click()
  assert.equal(selected(), 3)
  assert.equal(box.checked, true)
  assert.equal(box.indeterminate, false)

  box.click()
  assert.equal(selected(), 0)
  assert.equal(box.checked, false)

  w.document.querySelector('[data-email-id="m1"] > a').click()
  assert.equal(box.indeterminate, true)
  assert.equal(box.checked, false)
})
