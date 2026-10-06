// Runs assets/js/app.js in jsdom against the real filters popover markup.
// Run with `npm test` (or `task test`).
const { test } = require("node:test")
const assert = require("node:assert/strict")
const { execFileSync } = require("node:child_process")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const root = path.join(__dirname, "..", "..")
const renderedHTML = execFileSync("go", ["run", "./scripts/render-mail-filters"], { cwd: root, encoding: "utf8" })
const appJS = fs.readFileSync(path.join(root, "assets", "js", "app.js"), "utf8")

async function loadPage() {
  const dom = new JSDOM("<!doctype html><body>" + renderedHTML + "</body>", { runScripts: "outside-only", pretendToBeVisual: true })
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

function unreadToggle(w) {
  return w.document.querySelector("[data-mail-unread-toggle]")
}

function filterButton(w) {
  return w.document.querySelector("[data-mail-filter-button]")
}

function applyPopover(w) {
  w.document.querySelector("[data-mail-advanced-filter-form]").dispatchEvent(new w.Event("submit", { bubbles: true, cancelable: true }))
}

function statusValue(w) {
  return w.document.querySelector('[data-mail-tristate="status"]').getAttribute("data-mail-tristate-value")
}

test("Unread toggle sets status to unread and back to Any", async function () {
  const w = await loadPage()
  assert.equal(unreadToggle(w).getAttribute("aria-pressed"), "false")
  unreadToggle(w).click()
  assert.equal(statusValue(w), "unread")
  assert.equal(unreadToggle(w).getAttribute("aria-pressed"), "true")
  assert.equal(filterButton(w).dataset.active, "true") // readFilters().unread reached syncFilterButton
  unreadToggle(w).click()
  assert.equal(statusValue(w), "")
  assert.equal(unreadToggle(w).getAttribute("aria-pressed"), "false")
  assert.equal(filterButton(w).dataset.active, "false")
})

test("Unread toggle switches Read to Unread", async function () {
  const w = await loadPage()
  pick(w, "status", "read")
  unreadToggle(w).click()
  assert.equal(statusValue(w), "unread")
  assert.equal(unreadToggle(w).getAttribute("aria-pressed"), "true")
})

test("Unread toggle follows the popover status", async function () {
  const w = await loadPage()
  pick(w, "status", "unread")
  applyPopover(w)
  assert.equal(unreadToggle(w).getAttribute("aria-pressed"), "true")
  pick(w, "status", "read")
  applyPopover(w)
  assert.equal(unreadToggle(w).getAttribute("aria-pressed"), "false")
})

// Hover quick actions on list rows (real MailListCardItem / MailListTableItem markup).
async function clickRowAction(id, action) {
  const w = await loadPage()
  const calls = []
  w.fetch = function (url, init) {
    calls.push({ url: String(url), body: init && init.body ? JSON.parse(init.body) : null })
    return new Promise(function () {})
  }
  w.document.querySelector("[data-test-mail-list]").id = "mail-list-scroll"
  const row = w.document.querySelector('#mail-list-scroll .mail-list-item[data-email-id="' + id + '"]')
  let opened = 0
  row.querySelector(":scope > a").addEventListener("click", function () { opened++ })
  const button = row.querySelector('[data-mail-row-action="' + action + '"]')
  const event = new w.MouseEvent("click", { bubbles: true, cancelable: true })
  button.dispatchEvent(event)
  return { calls, opened, event, w }
}

test("Row Archive posts one thread target and does not open the message", async function () {
  const { calls, opened, event } = await clickRowAction("m1", "archive")
  assert.equal(opened, 0)
  assert.equal(event.defaultPrevented, true)
  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, "/api/messages/archive")
  assert.deepEqual(calls[0].body.targets, [{ id: "m1", thread: true }])
})

test("Row Delete sends a non-thread target to the delete endpoint", async function () {
  const { calls, opened } = await clickRowAction("m2", "delete")
  assert.equal(opened, 0)
  assert.equal(calls[0].url, "/api/messages/delete")
  assert.deepEqual(calls[0].body.targets, [{ id: "m2", thread: false }])
})

test("Row Mark as read posts no state; Mark as unread posts state=unread", async function () {
  const read = await clickRowAction("m1", "read")
  assert.equal(read.calls[0].url, "/api/messages/read")
  assert.equal(read.calls[0].body.state, undefined)
  assert.deepEqual(read.calls[0].body.targets, [{ id: "m1", thread: true }])
  const unread = await clickRowAction("m2", "unread")
  assert.equal(unread.calls[0].url, "/api/messages/read")
  assert.equal(unread.calls[0].body.state, "unread")
  assert.deepEqual(unread.calls[0].body.targets, [{ id: "m2", thread: false }])
  assert.equal(unread.opened, 0)
})

test("Table row actions use the same path", async function () {
  const { calls, opened } = await clickRowAction("m3", "archive")
  assert.equal(opened, 0)
  assert.deepEqual(calls[0].body.targets, [{ id: "m3", thread: false }])
})

// Keyboard shortcuts that remove a row must drop it from the list at once, not after the server answers.
async function pressKey(key, respond) {
  const w = await loadPage()
  w.Element.prototype.scrollIntoView = function () {} // jsdom lacks it
  const calls = []
  w.fetch = function (url, init) {
    if (!(init && init.body)) return new Promise(function () {}) // ignore unrelated GETs
    calls.push({ url: String(url), body: JSON.parse(init.body) })
    return respond ? respond() : new Promise(function () {})
  }
  w.document.querySelector("[data-test-mail-list]").id = "mail-list-scroll"
  // Shortcuts act only on a selected row; they no longer fall back to the top row.
  w.document.querySelector("#mail-list-scroll .mail-list-item > a").click()
  w.document.dispatchEvent(new w.KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }))
  return { w, calls }
}

function rowVisible(w, id) {
  const row = w.document.querySelector('#mail-list-scroll .mail-list-item[data-email-id="' + id + '"]')
  return !!row && row.style.display !== "none"
}

for (const [key, url] of [["e", "/api/messages/archive"], ["#", "/api/messages/delete"], ["Delete", "/api/messages/delete"]]) {
  test("Pressing " + key + " removes the row immediately while the request is pending", async function () {
    const { w, calls } = await pressKey(key)
    assert.equal(calls.length, 1)
    assert.equal(calls[0].url, url)
    const id = calls[0].body.targets[0].id
    assert.equal(rowVisible(w, id), false)
  })
}

test("A failed archive restores the row and shows an error toast", async function () {
  let reject
  const { w, calls } = await pressKey("e", function () { return new Promise(function (_, r) { reject = r }) })
  const id = calls[0].body.targets[0].id
  assert.equal(rowVisible(w, id), false)
  reject(new Error("offline"))
  await new Promise(function (resolve) { setTimeout(resolve, 20) })
  assert.equal(rowVisible(w, id), true)
  assert.ok(w.document.querySelector("#mail-action-error, [id^='mail-action-error']") || w.document.body.textContent.includes("Could not archive"))
})

test("A non-OK archive response restores the row", async function () {
  const { w, calls } = await pressKey("e", function () { return Promise.resolve({ ok: false, status: 500 }) })
  const id = calls[0].body.targets[0].id
  assert.equal(rowVisible(w, id), false)
  await new Promise(function (resolve) { setTimeout(resolve, 20) })
  assert.equal(rowVisible(w, id), true)
})
