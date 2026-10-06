// Destructive single-key shortcuts need a selected or open message; # in Trash asks first.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function setup(opts) {
  const sidebar = '<aside><a hx-get="/folder/f-trash" data-folder-role="trash"><span class="truncate">Trash</span></a></aside>'
  const rows = '<div id="mail-list-scroll" data-folder-id="' + (opts.folder || "f-inbox") + '">' +
    '<div class="mail-list-item" data-email-id="m1" data-position="0"><a href="#"></a></div>' +
    '<div class="mail-list-item" data-email-id="m2" data-position="1"><a href="#"></a></div></div>'
  const dom = new JSDOM("<!doctype html><body>" + sidebar + rows + "</body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const calls = []
  const confirms = []
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function (url, o) {
    calls.push({ url: url, body: o && o.body ? JSON.parse(o.body) : null })
    return Promise.resolve({ ok: true, json: function () { return Promise.resolve({ moved: [] }) } })
  }
  w.VirtualMailList = function () { return new Proxy({}, { get: function (_, prop) { return prop === "folderID" ? (opts.folder || "f-inbox") : function () { return Promise.resolve() } } }) }
  w.goferConfirm = function (title) { confirms.push(title); return Promise.resolve(!!opts.confirmYes) }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  return { w: w, calls: calls, confirms: confirms }
}
const press = function (w, key) { w.document.dispatchEvent(new w.KeyboardEvent("keydown", { key: key, bubbles: true, cancelable: true })) }
const tick = function () { return new Promise(function (r) { setTimeout(r, 5) }) }
const mutating = function (calls) { return calls.filter(function (c) { return /\/api\/messages\/(archive|delete|star|read)/.test(c.url) }) }

test("e # s u do nothing when no row is selected or open", async () => {
  const t = await setup({})
  ;["e", "#", "s", "u"].forEach(function (k) { press(t.w, k) })
  await tick()
  assert.equal(mutating(t.calls).length, 0)
  assert.equal(t.w.document.querySelectorAll("[data-mail-selected]").length, 0)
})

test("e archives the selected row", async () => {
  const t = await setup({})
  t.w.document.querySelector('[data-email-id="m2"] > a').click()
  press(t.w, "e")
  await tick()
  const archive = t.calls.find(function (c) { return c.url === "/api/messages/archive" })
  assert.ok(archive)
  assert.equal(archive.body.targets[0].id, "m2")
})

test("# in Trash asks before permanently deleting, and only deletes on confirm", async () => {
  const declined = await setup({ folder: "f-trash", confirmYes: false })
  declined.w.document.querySelector('[data-email-id="m1"] > a').click()
  press(declined.w, "#")
  await tick()
  assert.equal(declined.confirms.length, 1)
  assert.equal(mutating(declined.calls).length, 0)

  const accepted = await setup({ folder: "f-trash", confirmYes: true })
  accepted.w.document.querySelector('[data-email-id="m1"] > a').click()
  press(accepted.w, "#")
  await tick()
  assert.ok(accepted.calls.find(function (c) { return c.url === "/api/messages/delete" }))
})
