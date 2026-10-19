// List footer: "Stored on this device" plus how long ago mail last synced,
// filled in when a sync finishes (virtual-scroll.js updateSyncHeader).
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const root = path.join(__dirname, "..", "..")
const virtualScroll = fs.readFileSync(path.join(root, "assets", "js", "virtual-scroll.js"), "utf8")

function vsWindow(body) {
  const dom = new JSDOM("<!doctype html><html><body>" + (body || "") + "</body></html>", { runScripts: "outside-only" })
  dom.window.eval(virtualScroll)
  return dom.window
}

test("synced label reads as relative time", () => {
  const w = vsWindow()
  const now = 10 * 60 * 60 * 1000
  assert.equal(w.mailListSyncedLabel(0, now), "")
  assert.equal(w.mailListSyncedLabel(now - 30 * 1000, now), "synced just now")
  assert.equal(w.mailListSyncedLabel(now - 5 * 60 * 1000, now), "synced 5m ago")
  assert.equal(w.mailListSyncedLabel(now - 3 * 60 * 60 * 1000, now), "synced 3h ago")
})

test("marking a sync fills the footer, which stays blank before one", () => {
  const w = vsWindow('<p><span data-mail-list-synced></span></p>')
  const el = w.document.querySelector("[data-mail-list-synced]")
  w.mailListRenderSynced()
  assert.equal(el.textContent, "")
  w.mailListMarkSynced()
  assert.equal(el.textContent, " · synced just now")
  w.close()
})
