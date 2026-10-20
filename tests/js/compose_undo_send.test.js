// Send with an undo window closes the dialog at once; Undo cancels the send and reopens the same form.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

const dialogHTML = '<!doctype html><body><button id="sidebar-compose-btn"></button>' +
  '<div id="compose-dialog" data-tui-dialog><dialog data-tui-dialog-content open>' +
  '<form id="compose-form" data-compose-dirty="true"><input name="subject" value="Hello"><button data-compose-send>Send</button></form>' +
  "</dialog></div></body>"

const paneForm = '<div data-compose-pane><form id="compose-pane-form" data-compose-dirty="true"><input name="subject" value="Re: Hi"><button data-compose-send>Send</button></form></div>'
// An inline reply sits at the end of the open conversation (openInlineCompose).
const inlineHTML = '<!doctype html><body><button id="sidebar-compose-btn"></button><div id="main-content"><div id="mail-view">' +
  '<div class="mail-view-content"><p id="thread">Thread</p><div data-compose-inline>' + paneForm + "</div></div></div></div></body>"
// A pane replaces the message view.
const paneHTML = '<!doctype html><body><button id="sidebar-compose-btn"></button><div id="main-content"><div id="mail-view">' + paneForm + "</div></div></body>"

async function setup(cancelStatus, html) {
  html = html || dialogHTML
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
  return { w, calls, dialog, form: w.document.getElementById("compose-form") || w.document.getElementById("compose-pane-form") }
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

test("An inline reply leaves the conversation at once and comes back unchanged on Undo", async () => {
  const t = await setup(200, inlineHTML)
  t.w._sendComposeNow(t.form, true)
  await tick()
  const doc = t.w.document
  assert.equal(doc.getElementById("compose-pane-form"), null, "editor is off the page during the undo window")
  assert.ok(doc.getElementById("thread"), "the conversation stays in view")
  assert.match(doc.body.textContent, /Message sent/)
  doc.querySelector("[data-raven-toast-secondary]").click()
  await tick()
  const form = doc.getElementById("compose-pane-form")
  assert.ok(form, "editor is back")
  assert.equal(form.closest("[data-compose-inline]").parentNode.className, "mail-view-content")
  assert.equal(form.querySelector('[name="subject"]').value, "Re: Hi")
  assert.equal(form.dataset.composeSending, "false")
  t.w.close()
})

test("A pane closes to the empty message view and is restored on Undo", async () => {
  const t = await setup(200, paneHTML)
  t.w._sendComposeNow(t.form, true)
  await tick()
  const doc = t.w.document
  assert.equal(doc.getElementById("compose-pane-form"), null)
  assert.ok(doc.querySelector("#mail-view [data-mail-view-empty]"), "message view shows its empty state")
  doc.querySelector("[data-raven-toast-secondary]").click()
  await tick()
  assert.ok(doc.querySelector("#mail-view > [data-compose-pane] #compose-pane-form"))
  assert.equal(doc.querySelector("#mail-view [data-mail-view-empty]"), null)
  t.w.close()
})

test("A failed send brings the put-away editor back so it can be retried", async () => {
  const t = await setup(200, inlineHTML)
  t.w._sendComposeNow(t.form, true)
  await tick()
  assert.equal(t.w.document.getElementById("compose-pane-form"), null, "editor put away during the undo window")
  t.w.handleComposeSendResult("failed", { send_id: "s1" })
  const form = t.w.document.getElementById("compose-pane-form")
  assert.ok(form, "editor restored after failure")
  assert.equal(form.dataset.composeDirty, "true")
  t.w.close()
})

test("A reply pane shows the message it answered during the undo window", async () => {
  const html = paneHTML.replace("<div data-compose-pane>", '<div data-compose-pane data-compose-replaced-email="e42">')
  const t = await setup(200, html)
  const ajax = []
  t.w.htmx = { ajax: function (verb, url, opts) { ajax.push(verb + " " + url + " -> " + opts.target); return Promise.resolve() } }
  t.w._sendComposeNow(t.form, true)
  await tick()
  assert.ok(ajax.some(function (a) { return /^GET \/email\/e42/.test(a) && /#mail-view$/.test(a) }), "reopens the replied-to message: " + ajax.join(", "))
  t.w.document.querySelector("[data-raven-toast-secondary]").click()
  await tick()
  assert.ok(t.w.document.querySelector("#mail-view > [data-compose-pane] #compose-pane-form"), "Undo brings the reply back")
  t.w.close()
})
