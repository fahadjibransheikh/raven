// Esc / click-away on the compose dialog prompts when edited, and Esc in suggestions only closes them.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function setup(dirty) {
  const html = '<!doctype html><body><button id="sidebar-compose-btn" disabled></button>' +
    '<div id="compose-dialog" data-tui-dialog><dialog data-tui-dialog-content open>' +
    '<div id="compose-form" data-compose-dirty="' + dirty + '">' +
    '<input name="subject" value="' + (dirty === "true" ? "Hello" : "") + '">' +
    '<div data-compose-recipient-field data-recipient-name="to"><span data-compose-recipient-input></span>' +
    '<div data-compose-recipient-suggestions><button data-compose-recipient-suggestion></button></div></div>' +
    "</div></dialog></div></body>"
  const dom = new JSDOM(html, { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const choices = []
  const closed = []
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return Promise.resolve({ ok: true, json: function () { return Promise.resolve({}) } }) }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () {} } }) }
  w.goferChoice = function (title) { choices.push(title); return Promise.resolve("cancel") }
  w.tui = { dialog: { open: function () {}, close: function (id) { closed.push(id) } } }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  return { w: w, choices: choices, closed: closed, dialog: w.document.querySelector("dialog") }
}
const cancel = function (t) {
  const ev = new t.w.Event("cancel", { cancelable: true })
  t.dialog.dispatchEvent(ev)
  return ev
}
const tick = function () { return new Promise(function (r) { setTimeout(r, 5) }) }

test("Esc on an edited compose asks what to do and does not close the dialog by itself", async () => {
  const t = await setup("true")
  t.w.document.querySelector("[data-compose-recipient-suggestions]").hidden = true
  const ev = cancel(t)
  await tick()
  assert.equal(ev.defaultPrevented, true)
  assert.equal(t.choices.length, 1)
  assert.deepEqual(t.closed, [])
})

test("Esc on an untouched compose closes it and re-enables the Compose button", async () => {
  const t = await setup("false")
  t.w.document.querySelector("[data-compose-recipient-suggestions]").hidden = true
  cancel(t)
  await tick()
  assert.equal(t.choices.length, 0)
  assert.deepEqual(t.closed, ["compose-dialog"])
  assert.equal(t.w.document.getElementById("sidebar-compose-btn").disabled, false)
})

test("Esc with suggestions open closes only the suggestions", async () => {
  const t = await setup("true")
  const box = t.w.document.querySelector("[data-compose-recipient-suggestions]")
  box.hidden = false
  cancel(t)
  await tick()
  assert.equal(box.hidden, true)
  assert.equal(t.choices.length, 0)
  assert.deepEqual(t.closed, [])
})

test("c with a compose pane open focuses it instead of wiping the dialog draft", async () => {
  const t = await setup("true")
  t.dialog.removeAttribute("open")
  t.w.document.body.insertAdjacentHTML("beforeend", '<form id="compose-pane-form"><input name="subject" value="Draft"><div data-compose-editor contenteditable="true"></div></form>')
  t.w.openNewCompose()
  assert.equal(t.w.document.querySelector('#compose-form input[name="subject"]').value, "Hello")
  assert.equal(t.closed.length, 0)
})

test("Send reuses one send_key until the send fails, then mints a new one", async () => {
  const t = await setup("true")
  const form = t.w.document.getElementById("compose-form")
  form.querySelector("[data-compose-recipient-field]").remove()
  form.insertAdjacentHTML("beforeend", '<input name="to" value="a@b.co">')
  const keys = []
  t.w.fetch = function (url, o) {
    if (url === "/compose") keys.push(new URLSearchParams(o.body).get("send_key"))
    return Promise.resolve({ ok: url === "/compose", json: function () { return Promise.resolve({ send_id: "s1" }) } })
  }
  t.w.sendCompose(false)
  await tick()
  form.dataset.composeSending = "false" // what a "retrying" result does: Send is clickable again
  t.w.sendCompose(false)
  await tick()
  assert.equal(keys.length, 2)
  assert.ok(keys[0] && keys[0] === keys[1], "same key while the send is still in flight")
  t.w.handleComposeSendResult("failed", { send_id: "s1" })
  form.dataset.composeSending = "false"
  t.w.sendCompose(false)
  await tick()
  assert.notEqual(keys[2], keys[0])
  t.w.close() // stop the outgoing-status polling timer so the test process can exit
})
