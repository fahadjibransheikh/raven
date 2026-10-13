// The select-all menu picks loaded rows by state, and the banner offers to select
// every message the folder and filters match (fetched from /mail/folder/{id}/ids).
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

const ROWS = [
  { id: "m1", read: true, starred: false },
  { id: "m2", read: false, starred: true },
  { id: "m3", read: false, starred: false },
  { id: "m4", read: true, starred: true },
]

async function setup(opts) {
  opts = opts || {}
  const rows = ROWS.map(function (r, i) {
    return '<div class="mail-list-item" data-email-id="' + r.id + '" data-position="' + i + '" data-has-thread="' + (r.id === "m3") + '" data-is-read="' + r.read + '" data-starred="' + r.starred + '"><a href="#"></a></div>'
  }).join("")
  const dom = new JSDOM('<!doctype html><body>' +
    '<input type="checkbox" data-mail-select-all>' +
    '<button data-mail-select="read"></button><button data-mail-select="unread"></button><button data-mail-select="starred"></button><button data-mail-select="invert"></button><button data-mail-select="none"></button>' +
    '<button data-mail-selection-clear class="hidden"><span data-mail-selection-summary>0 selected</span></button><button data-mail-selection-action="star">Star</button>' +
    '<div data-mail-select-all-banner class="hidden"><span data-mail-select-all-banner-text></span><button data-mail-select-all-banner-action class="hidden"></button><button data-mail-select-all-banner-clear>Clear</button></div>' +
    '<div id="mail-list-scroll">' + rows + "</div></body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const calls = []
  const everyID = opts.everyID || ROWS.map(function (r) { return { id: r.id, thread: r.id === "m3" } })
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function (url, o) {
    calls.push({ url: String(url), body: o && o.body ? JSON.parse(o.body) : null })
    if (/\/ids\b/.test(String(url))) {
      return Promise.resolve({ ok: true, json: function () { return Promise.resolve({ ids: everyID, total: everyID.length, truncated: !!opts.truncated }) } })
    }
    return Promise.resolve({ ok: true, json: function () { return Promise.resolve({ moved: [] }) } })
  }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () { return Promise.resolve() } } }) }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  const fake = { folderID: "inbox", totalCount: opts.total || 4, displayTotalCount: opts.total || 4, cache: new Map(), indexById: new Map(), withFilterParams: function (u) { return u + "?unread=1" } }
  w.document.getElementById("mail-list-scroll")._virtualMailList = fake
  return { w: w, d: w.document, calls: calls }
}
const selected = function (d) {
  return Array.from(d.querySelectorAll(".mail-list-item")).filter(function (r) { return r.hasAttribute("data-mail-selected") }).map(function (r) { return r.dataset.emailId })
}
const pick = function (d, choice) { d.querySelector('[data-mail-select="' + choice + '"]').click() }
const tick = function () { return new Promise(function (r) { setTimeout(r, 10) }) }

test("the select-all menu picks loaded rows by state", async () => {
  const t = await setup()
  pick(t.d, "unread")
  assert.deepEqual(selected(t.d), ["m2", "m3"])
  pick(t.d, "invert")
  assert.deepEqual(selected(t.d), ["m1", "m4"])
  pick(t.d, "starred")
  assert.deepEqual(selected(t.d), ["m2", "m4"])
  pick(t.d, "read")
  assert.deepEqual(selected(t.d), ["m1", "m4"])
  pick(t.d, "none")
  assert.deepEqual(selected(t.d), [])
  assert.equal(t.d.querySelector("[data-mail-select-all]").indeterminate, false)
})

test("a menu choice with no matching loaded row leaves nothing selected", async () => {
  const t = await setup()
  pick(t.d, "unread")
  assert.deepEqual(selected(t.d), ["m2", "m3"])
  t.d.querySelectorAll(".mail-list-item")[1].remove()
  t.d.querySelectorAll(".mail-list-item")[1].remove()
  pick(t.d, "unread")
  assert.deepEqual(selected(t.d), [])
})

test("the banner appears for two or more selected rows and offers every message in the list", async () => {
  const t = await setup({ total: 120 })
  const banner = t.d.querySelector("[data-mail-select-all-banner]")
  const action = t.d.querySelector("[data-mail-select-all-banner-action]")
  const text = t.d.querySelector("[data-mail-select-all-banner-text]")

  t.d.querySelector('[data-email-id="m1"] > a').dispatchEvent(new t.w.MouseEvent("click", { bubbles: true, cancelable: true, ctrlKey: true }))
  assert.equal(banner.classList.contains("hidden"), true, "one message is not a bulk selection")

  pick(t.d, "unread")
  assert.equal(banner.classList.contains("hidden"), false)
  assert.equal(text.textContent, "2 selected.")
  assert.equal(action.classList.contains("hidden"), false)
  assert.equal(action.textContent, "Select all 120 in this list")

  action.click()
  await tick()
  const idsCall = t.calls.find(function (c) { return /\/mail\/folder\/inbox\/ids/.test(c.url) })
  assert.ok(idsCall, "fetches the ids for the folder with the list's filters")
  assert.match(idsCall.url, /unread=1/)
  assert.equal(text.textContent, "All 4 messages in this list are selected.")
  assert.equal(action.classList.contains("hidden"), true)
  assert.equal(t.d.querySelector("[data-mail-select-all]").checked, true)

  t.d.querySelector("[data-mail-select-all-banner-clear]").click()
  assert.deepEqual(selected(t.d), [])
  assert.equal(banner.classList.contains("hidden"), true)
})

test("a truncated whole-list selection says so", async () => {
  const t = await setup({ total: 20000, truncated: true })
  pick(t.d, "unread")
  t.d.querySelector("[data-mail-select-all-banner-action]").click()
  await tick()
  assert.equal(t.d.querySelector("[data-mail-select-all-banner-text]").textContent, "The first 4 messages in this list are selected.")
})

test("bulk actions on a whole-list selection go out in chunks and keep thread flags", async () => {
  const many = []
  for (let i = 0; i < 450; i++) many.push({ id: "x" + i, thread: i === 3 })
  const t = await setup({ total: 450, everyID: many })
  pick(t.d, "unread")
  t.d.querySelector("[data-mail-select-all-banner-action]").click()
  await tick()
  t.d.querySelector('[data-mail-selection-action="star"]').click()
  await tick()
  await tick()
  const stars = t.calls.filter(function (c) { return c.url === "/api/messages/star" })
  assert.deepEqual(stars.map(function (c) { return c.body.targets.length }), [200, 200, 50])
  assert.equal(stars[0].body.targets[3].thread, true)
  assert.equal(stars[0].body.targets[0].thread, false)
})
