// window.prompt is dead in the desktop WKWebView: Add label and Insert link use goferPrompt instead.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const root = path.join(__dirname, "..", "..", "assets", "js")
const appJS = fs.readFileSync(path.join(root, "app.js"), "utf8")
const ravenJS = fs.readFileSync(path.join(root, "raven.js"), "utf8")

test("goferPrompt resolves the typed text on OK or Enter and null on Cancel", async () => {
  const w = new JSDOM("<!doctype html><body></body>", { runScripts: "outside-only" }).window
  w.eval(ravenJS)
  let p = w.goferPrompt("Add label", "Label name", { confirmLabel: "Add label" })
  const input = w.document.querySelector(".compose-close-choice input")
  assert.ok(input, "prompt shows a text field")
  input.value = "Work"
  w.document.querySelector('[data-compose-close-action="ok"]').click()
  assert.equal(await p, "Work")

  p = w.goferPrompt("Add label", "Label name")
  const input2 = w.document.querySelector(".compose-close-choice input")
  input2.value = "Home"
  input2.dispatchEvent(new w.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }))
  assert.equal(await p, "Home")

  p = w.goferPrompt("Add label", "Label name")
  w.document.querySelector('[data-compose-close-action="cancel"]').click()
  assert.equal(await p, null)
  assert.equal(w.document.querySelector(".compose-close-choice"), null, "panel removed")
})

async function loadApp(body) {
  const dom = new JSDOM("<!doctype html><body>" + body + "</body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const calls = []
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function (url, o) {
    calls.push({ url: url, body: o && o.body ? JSON.parse(o.body) : null })
    return Promise.resolve({ ok: true, json: function () { return Promise.resolve({}) } })
  }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () { return Promise.resolve() } } }) }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  return { w: w, calls: calls }
}
const tick = function () { return new Promise(function (r) { setTimeout(r, 5) }) }

test("Add label asks in-app and posts the label", async () => {
  const t = await loadApp('<div id="mail-list-scroll"><div class="mail-list-item" data-email-id="m1"><a href="#"></a></div></div>')
  let asked = null
  t.w.prompt = function () { throw new Error("window.prompt must not be used") }
  t.w.goferPrompt = function (title) { asked = title; return Promise.resolve("  Work ") }
  t.w.performMailAction("label", ["m1"], { keepSelection: true })
  await tick()
  assert.equal(asked, "Add label")
  const post = t.calls.find(function (c) { return c.url === "/api/messages/label" })
  assert.equal(post.body.label, "Work")

  t.calls.length = 0
  t.w.promptLabelMessage("m1", false)
  await tick()
  assert.equal(t.calls.find(function (c) { return /\/m1\/label$/.test(c.url) }).body.label, "Work")
})

test("Insert link asks in-app, normalises the URL and creates the link", async () => {
  const t = await loadApp('<form id="compose-form"><div data-compose-editor contenteditable="true">hi</div></form>')
  const links = []
  t.w.document.execCommand = function (cmd, _, value) { if (cmd === "createLink") links.push(value); return true }
  t.w.prompt = function () { throw new Error("window.prompt must not be used") }
  t.w.goferPrompt = function () { return Promise.resolve("example.com") }
  t.w.composeCreateLink(t.w.document.querySelector("[data-compose-editor]"))
  await tick()
  assert.deepEqual(links, ["https://example.com"])
})
