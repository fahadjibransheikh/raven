// Links inside the sandboxed message frame are opened by the parent page,
// and only when the request comes from a real message frame.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function setup() {
  const dom = new JSDOM('<!doctype html><body><iframe data-email-body-frame data-email-id="1"></iframe><iframe id="other"></iframe></body>', { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return Promise.resolve({ ok: true, json: function () { return Promise.resolve({}) } }) }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () {} } }) }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  const opened = []
  w.open = function () { opened.push(Array.prototype.slice.call(arguments)) }
  const send = function (source, href) {
    w.dispatchEvent(new w.MessageEvent("message", { data: { type: "emailLinkClick", href: href }, source: source.contentWindow }))
  }
  return { w, opened, send, frame: w.document.querySelector("[data-email-body-frame]"), other: w.document.getElementById("other") }
}

test("web and mail links from a message frame open in a new noopener window", async () => {
  const { opened, send, frame } = await setup()
  send(frame, "https://example.com/a")
  send(frame, "mailto:a@example.com")
  assert.deepEqual(opened.map(function (o) { return [o[0], o[1], o[2]] }), [
    ["https://example.com/a", "_blank", "noopener,noreferrer"],
    ["mailto:a@example.com", "_blank", "noopener,noreferrer"],
  ])
})

test("other schemes and foreign senders never open a window", async () => {
  const { opened, send, frame, other } = await setup()
  send(frame, "javascript:alert(1)")
  send(frame, "data:text/html,x")
  send(frame, "/api/inline-content/1/x")
  send(other, "https://example.com/")
  assert.equal(opened.length, 0)
})

test("keystrokes from a message frame are replayed on the page, from real frames only", async () => {
  const { w, send, frame, other } = await setup()
  const keys = []
  w.document.addEventListener("keydown", function (e) { keys.push(e.key + (e.shiftKey ? "+shift" : "")) })
  const sendKey = function (source, key, shiftKey) {
    w.dispatchEvent(new w.MessageEvent("message", { data: { type: "emailKeydown", key: key, shiftKey: !!shiftKey }, source: source.contentWindow }))
  }
  sendKey(frame, "e")
  sendKey(frame, "?", true)
  sendKey(other, "e")
  sendKey(frame, "")
  sendKey(frame, "x".repeat(40))
  assert.deepEqual(keys, ["e", "?+shift"])
})

test("the frame script forwards keys but not those typed into form fields", () => {
  const src = fs.readFileSync(path.join(__dirname, "..", "..", "internal", "handler", "handler.go"), "utf8")
  const script = /func emailExternalLinksScript\(\) \[\]byte \{\s*return \[\]byte\(`<script>([\s\S]*?)<\/script>`\)/.exec(src)[1]
  const dom = new JSDOM('<!doctype html><body><input id="f"><p id="p">hi</p></body>', { runScripts: "outside-only" })
  const w = dom.window
  const posted = []
  w.addEventListener("message", function (e) { posted.push(e.data) })
  w.eval(script)
  const press = function (el, key) { el.dispatchEvent(new w.KeyboardEvent("keydown", { key: key, bubbles: true })) }
  press(w.document.getElementById("p"), "e")
  press(w.document.getElementById("f"), "r")
  return new Promise(function (resolve) {
    setTimeout(function () {
      assert.deepEqual(posted.map(function (d) { return d.type + ":" + d.key }), ["emailKeydown:e"])
      resolve()
    }, 20)
  })
})
