// Archiving offers an Undo toast that moves the message back to the folder the server reported.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function setup(responses) {
  const rows = '<div id="mail-list-scroll" data-folder-id="f-inbox"><div class="mail-list-item" data-email-id="m1" data-account-id="a"><a href="#"></a></div></div>'
  const dom = new JSDOM("<!doctype html><body>" + rows + "</body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const calls = []
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function (url, opts) {
    calls.push({ url: url, body: opts && opts.body ? JSON.parse(opts.body) : null })
    const data = responses[url] || {}
    return Promise.resolve({ ok: true, json: function () { return Promise.resolve(data) } })
  }
  w.VirtualMailList = function () { return new Proxy({}, { get: function (_, prop) { return prop === "folderID" ? "f-inbox" : function () { return Promise.resolve() } } }) }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  return { w: w, calls: calls }
}
const tick = function () { return new Promise(function (r) { setTimeout(r, 5) }) }

test("archive shows Undo and undo posts a move back to the source folder", async () => {
  const t = await setup({ "/api/messages/archive": { updated: 1, moved: [{ id: "m1", thread: false, from: "f-inbox" }] } })
  t.w.performMailAction("archive", ["m1"], { keepSelection: true })
  await tick()
  const toast = t.w.document.getElementById("mail-undo-toast")
  assert.ok(toast, "undo toast shown")
  assert.match(toast.textContent, /Archived/)
  toast.querySelector("[data-gofer-toast-secondary]").click()
  await tick()
  const undo = t.calls.find(function (c) { return c.url === "/api/messages/move" })
  assert.ok(undo, "undo request sent")
  assert.equal(undo.body.folder_id, "f-inbox")
  assert.equal(undo.body.targets[0].id, "m1")
})

test("no Undo toast when the server reports nothing moved (permanent delete)", async () => {
  const t = await setup({ "/api/messages/delete": { updated: 1, moved: [] } })
  t.w.performMailAction("delete", ["m1"], { keepSelection: true })
  await tick()
  assert.equal(t.w.document.getElementById("mail-undo-toast"), null)
})
