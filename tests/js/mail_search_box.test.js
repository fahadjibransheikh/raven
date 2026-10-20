// The search box keeps the committed query so it can be edited, and is the only source of
// the plain query (no "Search:" chip, no doubling when the box is edited and run again).
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function setup() {
  const html = '<input data-mail-search-input><span data-mail-search-enter-hint hidden></span>' +
    '<button data-mail-filter-button><span data-mail-filter-count class="hidden"></span></button>' +
    '<form data-mail-advanced-filter-form><input name="from"></form>' +
    '<div id="mail-list-scroll" data-folder-id="f-inbox"></div>'
  const dom = new JSDOM("<!doctype html><body>" + html + "</body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const applied = []
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return Promise.resolve({ ok: true, json: function () { return Promise.resolve({}) } }) }
  w.VirtualMailList = function () {
    return new Proxy({}, { get: function (_, prop) {
      if (prop === "folderID") return "f-inbox"
      if (prop === "selectedEmailId" || prop === "filters") return null
      if (prop === "applyFilters") return function (f) { applied.push(f); return Promise.resolve() }
      return function () { return Promise.resolve() }
    } })
  }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  return { w: w, applied: applied, box: w.document.querySelector("[data-mail-search-input]") }
}

function enter(t, text) {
  t.box.value = text
  t.box.dispatchEvent(new t.w.KeyboardEvent("keydown", { key: "Enter", bubbles: true }))
}

const settle = () => new Promise(function (r) { setTimeout(r, 400) })

test("search keeps the plain words in the box and pulls keywords into filters", async () => {
  const t = await setup()
  enter(t, "budget starred")
  await settle()
  assert.equal(t.box.value, "budget")
  const last = t.applied[t.applied.length - 1]
  assert.equal(last.query, "budget")
  assert.equal(t.w.document.querySelector('[data-mail-active-pill="q"]'), null)
})

test("editing the query and searching again replaces it instead of appending", async () => {
  const t = await setup()
  enter(t, "budget")
  await settle()
  enter(t, "budget q4")
  await settle()
  assert.equal(t.box.value, "budget q4")
  assert.equal(t.applied[t.applied.length - 1].query, "budget q4")
})

test("an operator typed before Enter isn't searched as text", async () => {
  const t = await setup()
  t.box.value = "budget from:maya@example.net"
  t.box.dispatchEvent(new t.w.Event("input", { bubbles: true }))
  await new Promise(function (r) { setTimeout(r, 1200) })
  assert.equal(t.applied[t.applied.length - 1].query, "budget")
})

test("Enter turns from: into the From filter and leaves no text query", async () => {
  const t = await setup()
  enter(t, "from:maya@example.net")
  await settle()
  const last = t.applied[t.applied.length - 1]
  assert.equal(last.from, "maya@example.net")
  assert.equal(last.query, "")
  assert.equal(t.box.value, "")
})

test("typing an operator shows the Enter hint, and plain text doesn't count as a filter", async () => {
  const t = await setup()
  const hint = t.w.document.querySelector("[data-mail-search-enter-hint]")
  t.box.value = "from:maya"
  t.box.dispatchEvent(new t.w.Event("input", { bubbles: true }))
  assert.equal(hint.hidden, false)
  t.box.value = "budget"
  t.box.dispatchEvent(new t.w.Event("input", { bubbles: true }))
  assert.equal(hint.hidden, true)
  await new Promise(function (r) { setTimeout(r, 1200) })
  assert.ok(t.w.document.querySelector("[data-mail-filter-count]").classList.contains("hidden"))
})
