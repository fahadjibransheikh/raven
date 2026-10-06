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

test("new compose focuses To, then Subject when To is filled, then the body", async () => {
  const dom = new JSDOM('<!doctype html><body><form id="compose-form"><div data-recipient-name="to"><span data-compose-recipient-input></span></div>' +
    '<input name="to"><input name="subject"><div data-compose-editor contenteditable="true"></div></form></body>', { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return Promise.resolve({ ok: true, json: function () { return Promise.resolve({}) } }) }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  w.tui = { dialog: { open: function () {} } }
  const d = w.document
  const focused = []
  d.querySelector("[data-compose-recipient-input]").focus = function () { focused.push("to") }
  d.querySelector('input[name="subject"]').focus = function () { focused.push("subject") }
  d.querySelector("[data-compose-editor]").focus = function () { focused.push("body") }
  w.openNewCompose()
  assert.equal(focused[focused.length - 1], "to")
  d.querySelector('input[name="to"]').value = "a@b.co"
  w.focusComposePrefill(d.getElementById("compose-form"), "new")
  assert.equal(focused[focused.length - 1], "subject")
  d.querySelector('input[name="subject"]').value = "Hi"
  w.focusComposePrefill(d.getElementById("compose-form"), "new")
  assert.equal(focused[focused.length - 1], "body")
})
