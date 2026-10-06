// "Move to..." lists only the account's own folders and posts the chosen one to the bulk move endpoint.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function setup() {
  const sidebar =
    '<aside><div data-sidebar-account="acc1">' +
    '<a hx-get="/folder/f-inbox"><span class="truncate">Inbox</span></a>' +
    '<a hx-get="/folder/f-archive"><span class="truncate">Archive</span></a></div>' +
    '<div data-sidebar-account="acc2"><a hx-get="/folder/g-inbox"><span class="truncate">Other inbox</span></a></div></aside>'
  const rows = '<div id="mail-list-scroll" data-folder-id="f-inbox">' +
    '<div class="mail-list-item" data-email-id="m1" data-account-id="acc1"><a href="#"></a></div>' +
    '<div class="mail-list-item" data-email-id="m2" data-account-id="acc1"><a href="#"></a></div>' +
    '<div class="mail-list-item" data-email-id="m3" data-account-id="acc2"><a href="#"></a></div></div>'
  const dom = new JSDOM("<!doctype html><body>" + sidebar + rows + "</body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const calls = []
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function (url, opts) {
    calls.push({ url: url, body: opts && opts.body ? JSON.parse(opts.body) : null })
    return Promise.resolve({ ok: true, json: function () { return Promise.resolve({ moved: [] }) } })
  }
  w.VirtualMailList = function () { return new Proxy({}, { get: function (_, prop) { return prop === "folderID" ? "f-inbox" : function () { return Promise.resolve() } } }) }
  let picked = null
  w.RavenPalette = { pick: function (label, items) { picked = items } }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  return { w: w, calls: calls, picked: function () { return picked } }
}

test("move offers the account's folders except the current one, then posts the choice", async () => {
  const t = await setup()
  t.w.performMailAction("move", ["m1", "m2"])
  assert.deepEqual(Array.from(t.picked(), function (i) { return i.label }), ["Archive"])
  t.picked()[0].run()
  await new Promise(function (r) { setTimeout(r, 0) })
  const move = t.calls.find(function (c) { return c.url === "/api/messages/move" })
  assert.ok(move, "move request sent")
  assert.equal(move.body.folder_id, "f-archive")
  assert.deepEqual(Array.from(move.body.targets, function (x) { return x.id }), ["m1", "m2"])
})

test("move refuses a selection spanning two accounts", async () => {
  const t = await setup()
  t.w.performMailAction("move", ["m1", "m3"])
  assert.equal(t.picked(), null)
  assert.ok(t.w.document.getElementById("mail-action-error"))
})
