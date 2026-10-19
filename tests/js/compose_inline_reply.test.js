// Replies open below the open conversation ("thread" view), not over it.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

const PANE = '<div class="flex flex-col h-full p-2" data-compose-pane><div><h2>New Message</h2>' +
  '<form id="compose-pane-form"><input name="to"><input name="subject"><div data-compose-editor contenteditable="true"></div></form></div></div>'

async function setup(views) {
  const dom = new JSDOM('<!doctype html><body><div id="main-content"><div id="mail-list"></div>' +
    '<div id="mail-view"><div class="mail-view-content"><p data-thread-message>Original message</p></div><div id="reply-bar"></div></div></div>' +
    '<form id="compose-form"><input name="to"><div data-compose-editor contenteditable="true"></div></form></body>', { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: false, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function (url) {
    if (url === "/compose/pane") return Promise.resolve({ ok: true, text: function () { return Promise.resolve(PANE) } })
    return Promise.resolve({ ok: true, json: function () { return Promise.resolve({}) }, text: function () { return Promise.resolve("") } })
  }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  w.RavenSettings = { get: function (key) { return views[key] || null }, set: function () {} }
  let dialogOpened = false
  w.tui = { dialog: { open: function () { dialogOpened = true }, close: function () {} } }
  return { w, dialogOpened: function () { return dialogOpened } }
}

const settle = () => new Promise(function (resolve) { setTimeout(resolve, 20) })

test("a reply opens below the conversation and closing it keeps the conversation", async () => {
  const { w, dialogOpened } = await setup({ default_reply_compose_view: "thread", default_new_compose_view: "dialog" })
  const d = w.document
  w._openComposePrefill({ to: "a@b.co", subject: "Re: Hi", body: "", html_body: "" }, "reply")
  await settle()
  const host = d.querySelector("#mail-view .mail-view-content [data-compose-inline]")
  assert.ok(host, "reply editor mounted inside the conversation")
  assert.ok(d.querySelector("[data-thread-message]"), "conversation still shown")
  assert.equal(host.querySelector("h2").textContent, "Reply")
  assert.ok(host.querySelector("#compose-pane-form"))
  assert.equal(dialogOpened(), false)

  w.setMailViewEmpty(true) // what send, schedule and discard call to close the editor
  assert.equal(d.querySelector("[data-compose-inline]"), null)
  assert.ok(d.querySelector("[data-thread-message]"), "closing the reply keeps the conversation")
})

test("full view moves the inline reply into the message view", async () => {
  const { w } = await setup({ default_reply_compose_view: "thread" })
  const d = w.document
  w._openComposePrefill({ to: "a@b.co", body: "", html_body: "" }, "forward")
  await settle()
  assert.equal(d.querySelector("[data-compose-inline] h2").textContent, "Forward")
  w.expandComposeFullWidth()
  const pane = d.querySelector("#mail-view > [data-compose-pane]")
  assert.ok(pane, "editor now fills the message view")
  assert.equal(d.querySelector("[data-compose-inline]"), null)
  assert.ok(pane.querySelector("#compose-pane-form"))
})

test("new mail never uses the thread view, and replies fall back to the dialog with no conversation open", async () => {
  const { w, dialogOpened } = await setup({ default_reply_compose_view: "thread", default_new_compose_view: "thread" })
  assert.equal(w.composeViewPreference("new"), "dialog")
  assert.equal(w.composeViewPreference("reply"), "thread")
  w.document.querySelector(".mail-view-content").remove()
  w._openComposePrefill({ to: "a@b.co", body: "", html_body: "" }, "reply")
  await settle()
  assert.equal(w.document.querySelector("[data-compose-inline]"), null)
  assert.equal(dialogOpened(), true)
})
