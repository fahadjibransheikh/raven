// A dirty inline compose is never dropped silently by navigation.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function setup(action) {
  const html = '<!doctype html><body><button id="sidebar-compose-btn" disabled></button>' +
    '<aside><a id="folder" hx-get="/folder/inbox" href="/folder/inbox">Inbox</a>' +
    '<a id="contacts" data-sidebar-contacts-link href="/contacts">Contacts</a></aside>' +
    '<div id="mail-view"><div data-compose-pane><form id="compose-pane-form" data-compose-dirty="true">' +
    '<input name="subject" value="Hello"></form></div></div></body>'
  const dom = new JSDOM(html, { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const choices = []
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return Promise.resolve({ ok: true, json: function () { return Promise.resolve({}) } }) }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () {} } }) }
  w.goferChoice = function (title) { choices.push(title); return Promise.resolve(action) }
  w.tui = { dialog: { open: function () {}, close: function () {} } }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  return { w: w, choices: choices, view: w.document.getElementById("mail-view") }
}
const tick = function () { return new Promise(function (r) { setTimeout(r, 10) }) }
const hasPane = function (t) { return !!t.view.querySelector("[data-compose-pane]") }
const confirmEvent = function (t, issued) {
  const ev = new t.w.CustomEvent("htmx:confirm", { bubbles: true, cancelable: true,
    detail: { target: t.view, elt: t.w.document.getElementById("folder"), issueRequest: function () { issued.push(1) } } })
  t.w.document.getElementById("folder").dispatchEvent(ev)
  return ev
}

test("setMailViewEmpty leaves a dirty compose pane alone unless forced", async () => {
  const t = await setup("cancel")
  t.w.setMailViewEmpty()
  assert.equal(hasPane(t), true)
  t.w.setMailViewEmpty(true)
  assert.equal(hasPane(t), false)
})

test("a request into #mail-view prompts first and only proceeds on discard", async () => {
  const t = await setup("cancel")
  const issued = []
  const ev = confirmEvent(t, issued)
  await tick()
  assert.equal(ev.defaultPrevented, true)
  assert.equal(t.choices.length, 1)
  assert.equal(issued.length, 0)
  assert.equal(hasPane(t), true)

  const t2 = await setup("discard")
  const issued2 = []
  confirmEvent(t2, issued2)
  await tick()
  assert.equal(issued2.length, 1)
})

test("a clean compose does not prompt", async () => {
  const t = await setup("cancel")
  t.w.document.getElementById("compose-pane-form").dataset.composeDirty = "false"
  const issued = []
  const ev = confirmEvent(t, issued)
  assert.equal(ev.defaultPrevented, false)
  assert.equal(t.choices.length, 0)
})

test("folder click on a dirty pane is held until the user decides", async () => {
  const t = await setup("cancel")
  t.w.document.getElementById("folder").click()
  await tick()
  assert.equal(t.choices.length, 1)
  assert.equal(hasPane(t), true)

  // Discard: the real folder handler then runs once and clears the pane, with no second prompt.
  const t2 = await setup("discard")
  t2.w.document.getElementById("folder").click()
  await tick()
  assert.equal(t2.choices.length, 1)
  assert.equal(hasPane(t2), false)
})
