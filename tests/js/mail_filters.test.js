// Runs assets/js/app.js in jsdom against the real filters popover markup.
// Run with `npm test` (or `task test`).
const { test } = require("node:test")
const assert = require("node:assert/strict")
const { execFileSync } = require("node:child_process")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const root = path.join(__dirname, "..", "..")
const popoverHTML = execFileSync("go", ["run", "./scripts/render-mail-filters"], { cwd: root, encoding: "utf8" })
const appJS = fs.readFileSync(path.join(root, "assets", "js", "app.js"), "utf8")

async function loadPage() {
  const dom = new JSDOM("<!doctype html><body>" + popoverHTML + "</body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  // Browser APIs app.js touches at startup that jsdom lacks.
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.fetch = function () { return new Promise(function () {}) }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  return w
}

function pick(w, name, value) {
  w.document.querySelector('[data-mail-tristate="' + name + '"] [data-mail-tristate-option="' + value + '"]').click()
}

function applyCount(w) {
  const counter = w.document.querySelector("[data-mail-advanced-filter-count]")
  return counter.classList.contains("hidden") ? 0 : Number(counter.textContent)
}

const options = [
  ["status", "unread"], ["status", "read"],
  ["attachments", "yes"], ["attachments", "no"],
  ["tags", "yes"], ["tags", "no"],
  ["threads", "yes"], ["threads", "no"],
]

for (const [name, value] of options) {
  test("Apply count includes " + name + "=" + value, async function () {
    const w = await loadPage()
    assert.equal(applyCount(w), 0)
    pick(w, name, value)
    assert.equal(applyCount(w), 1)
    pick(w, name, "")
    assert.equal(applyCount(w), 0)
  })
}

test("Apply count adds one per tri-state group and Clear all resets it", async function () {
  const w = await loadPage()
  pick(w, "status", "unread")
  pick(w, "attachments", "yes")
  pick(w, "tags", "no")
  pick(w, "threads", "yes")
  assert.equal(applyCount(w), 4)
  w.document.querySelector("[data-mail-advanced-filter-clear]").click()
  assert.equal(applyCount(w), 0)
})
