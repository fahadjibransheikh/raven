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
