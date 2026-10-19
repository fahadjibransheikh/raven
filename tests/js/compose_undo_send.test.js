// Send with an undo window closes the dialog at once; Undo cancels the send and reopens the same form.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function setup(cancelStatus) {
  const html = '<!doctype html><body><button id="sidebar-compose-btn"></button>' +
    '<div id="compose-dialog" data-tui-dialog><dialog data-tui-dialog-content open>' +
    '<form id="compose-form" data-compose-dirty="true"><input name="subject" value="Hello"><button data-compose-send>Send</button></form>' +
    "</dialog></div></body>"
  const dom = new JSDOM(html, { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const calls = []
  const dialog = []
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () {} } }) }
  w.tui = { dialog: { open: function (id) { dialog.push("open " + id) }, close: function (id) { dialog.push("close " + id) } } }
  w.console.warn = function () {}
  w.fetch = function (url, opts) {
    calls.push((opts && opts.method || "GET") + " " + url)
    if (url === "/compose") {
      return Promise.resolve({ ok: true, status: 202, json: function () { return Promise.resolve({ status: "sending", send_id: "s1", undo_seconds: "10" }) } })
    }
    if (/\/cancel$/.test(url)) {
      return Promise.resolve({ ok: cancelStatus === 200, status: cancelStatus, json: function () { return Promise.resolve({}) } })
    }
    return Promise.resolve({ ok: true, status: 200, json: function () { return Promise.resolve({ id: "s1", status: "pending" }) } })
  }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  return { w, calls, dialog, form: w.document.getElementById("compose-form") }
}

const tick = function () { return new Promise(function (r) { setTimeout(r, 20) }) }

test("Undo cancels the held send and reopens the unchanged compose form", async () => {
  const t = await setup(200)
  t.w._sendComposeNow(t.form, false)
  await tick()
  assert.deepEqual(t.dialog, ["close compose-dialog"])
  const undo = t.w.document.querySelector("[data-raven-toast-secondary]")
  assert.ok(undo, "undo button shown")
  undo.click()
  await tick()
  assert.ok(t.calls.includes("POST /api/outgoing-sends/s1/cancel"))
  assert.deepEqual(t.dialog, ["close compose-dialog", "open compose-dialog"])
  assert.equal(t.form.querySelector('[name="subject"]').value, "Hello")
  assert.equal(t.form.dataset.composeSending, "false")
  assert.equal(t.form.dataset.composeSendKey, undefined)
  t.w.close()
})

test("Undo after the worker claimed the send leaves the dialog closed", async () => {
  const t = await setup(409)
  t.w._sendComposeNow(t.form, false)
  await tick()
  t.w.document.querySelector("[data-raven-toast-secondary]").click()
  await tick()
  assert.deepEqual(t.dialog, ["close compose-dialog"])
  assert.match(t.w.document.body.textContent, /Too late to undo/)
  t.w.close()
})
