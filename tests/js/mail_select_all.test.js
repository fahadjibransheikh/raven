// The select-all checkbox selects every rendered row and is tri-state.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

test("select-all toggles every rendered row and reports indeterminate", async () => {
  const rows = [1, 2, 3].map(function (n) { return '<div class="mail-list-item" data-email-id="m' + n + '"><a href="#"></a></div>' }).join("")
  const dom = new JSDOM('<!doctype html><body><input type="checkbox" data-mail-select-all><button data-mail-selection-clear class="hidden"><span data-mail-selection-summary>0 selected</span></button><button data-mail-selection-action>Archive</button><div id="mail-list-scroll">' + rows + "</div></body>", { runScripts: "outside-only", pretendToBeVisual: true })
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

  // A ctrl/cmd-click is an explicit selection.
  w.document.querySelector('[data-email-id="m1"] > a').dispatchEvent(new w.MouseEvent("click", { bubbles: true, cancelable: true, ctrlKey: true }))
  assert.equal(box.indeterminate, true)
  assert.equal(box.checked, false)
})

test("opening a message is not shown as a bulk selection but still feeds the toolbar actions", async () => {
  const rows = [1, 2, 3].map(function (n) { return '<div class="mail-list-item" data-email-id="m' + n + '"><a href="#"></a></div>' }).join("")
  const dom = new JSDOM('<!doctype html><body><input type="checkbox" data-mail-select-all><button data-mail-selection-clear class="hidden"><span data-mail-selection-summary>0 selected</span></button><button data-mail-selection-action>Archive</button><div id="mail-list-scroll">' + rows + "</div></body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return Promise.resolve({ ok: true, json: function () { return Promise.resolve({}) } }) }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () {} } }) }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  const d = w.document
  const box = d.querySelector("[data-mail-select-all]")
  const summary = d.querySelector("[data-mail-selection-summary]")
  const clear = d.querySelector("[data-mail-selection-clear]")
  const ctrlClick = function (id) { d.querySelector('[data-email-id="' + id + '"] > a').dispatchEvent(new w.MouseEvent("click", { bubbles: true, cancelable: true, ctrlKey: true })) }

  d.querySelector('[data-email-id="m1"] > a').click()
  assert.equal(summary.textContent, "0 selected")
  assert.equal(clear.classList.contains("hidden"), true)
  assert.equal(box.indeterminate, false)
  assert.equal(d.querySelector("[data-mail-selection-action]").disabled, false, "toolbar still acts on the open message")

  // Ticking the open row (or another) is an explicit selection and counts.
  ctrlClick("m1")
  assert.equal(summary.textContent, "1 selected")
  assert.equal(clear.classList.contains("hidden"), false)
  assert.equal(box.indeterminate, true)

  // Select-all after opening selects everything, and Clear empties it.
  d.querySelector('[data-email-id="m2"] > a').click()
  box.click()
  assert.equal(summary.textContent, "3 selected")
  clear.click()
  assert.equal(summary.textContent, "0 selected")
  assert.equal(d.querySelector("[data-mail-selection-action]").disabled, true)
})
