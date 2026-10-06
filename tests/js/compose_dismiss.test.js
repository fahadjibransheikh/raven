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
