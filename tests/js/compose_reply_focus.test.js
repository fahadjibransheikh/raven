// Reply must focus the editor after the (hidden) compose dialog is opened.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

test("dialog reply focuses the editor only after the dialog is open", async () => {
  const dom = new JSDOM('<!doctype html><body><form id="compose-form"><input name="to"><div data-compose-editor contenteditable="true"></div></form></body>', { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return Promise.resolve({ ok: true, json: function () { return Promise.resolve({}) } }) }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  let opened = false
  const focusedWhileOpen = []
  w.tui = { dialog: { open: function () { opened = true } } }
  w.document.querySelector("[data-compose-editor]").focus = function () { focusedWhileOpen.push(opened) }
  w._openComposePrefill({ body: "", html_body: "", _composeDirty: "true" }, "reply")
  assert.ok(focusedWhileOpen.length > 0)
  assert.equal(focusedWhileOpen[focusedWhileOpen.length - 1], true)
})
