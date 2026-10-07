// Messages from the sandboxed body frame are trusted only as far as the frame
// that sent them: a frame can resize itself and show its own banner, nothing else.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")
const inviteJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "invite-card.js"), "utf8")

async function setup() {
  const dom = new JSDOM('<!doctype html><body>' +
    '<iframe data-email-body-frame data-email-id="1" class="opacity-0"></iframe><div data-email-body-loading="1"></div><div data-remote-content-banner="1" class="hidden"></div>' +
    '<iframe data-email-body-frame data-email-id="2" class="opacity-0"></iframe><div data-remote-content-banner="2" class="hidden"></div>' +
    '<iframe id="stranger"></iframe></body>', { url: "http://localhost/", runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return Promise.resolve({ ok: true, status: 204, text: function () { return Promise.resolve("") }, json: function () { return Promise.resolve({}) } }) }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () {} } }) }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  const frames = w.document.querySelectorAll("[data-email-body-frame]")
  const post = function (frame, data) { w.dispatchEvent(new w.MessageEvent("message", { data: data, source: frame.contentWindow })) }
  return { w, d: w.document, f1: frames[0], f2: frames[1], stranger: w.document.getElementById("stranger"), post }
}

test("a frame resizes only itself, whatever emailId it claims", async () => {
  const { d, f1, f2, post } = await setup()
  post(f1, { type: "emailBodyResize", emailId: "2", height: 321 })
  assert.equal(f1.style.height, "321px")
  assert.ok(!f1.classList.contains("opacity-0"))
  assert.equal(f2.style.height, "", "another message's frame must not be touched")
  assert.equal(d.querySelector("[data-email-body-loading]"), null)
})

test("a window that is not a message frame cannot resize or raise banners", async () => {
  const { d, f1, stranger, post } = await setup()
  post(stranger, { type: "emailBodyResize", emailId: "1", height: 999 })
  post(stranger, { type: "remoteContentBlocked", emailId: "1" })
  assert.equal(f1.style.height, "")
  assert.ok(d.querySelector('[data-remote-content-banner="1"]').classList.contains("hidden"))
})

test("a hostile emailId never reaches a selector and does not throw", async () => {
  const { w, f1, post } = await setup()
  assert.doesNotThrow(() => {
    post(f1, { type: "emailBodyResize", emailId: '"] , body , ["x', height: 10 })
    post(f1, { type: "remoteContentBlocked", emailId: "1\"]x" })
  })
  assert.equal(f1.style.height, "10px")
  void w
})

test("only a frame's own banner is shown", async () => {
  const { d, f2, post } = await setup()
  post(f2, { type: "remoteContentBlocked", emailId: "1" })
  assert.ok(!d.querySelector('[data-remote-content-banner="2"]').classList.contains("hidden"))
  assert.ok(d.querySelector('[data-remote-content-banner="1"]').classList.contains("hidden"))
})

test("invite card only requests plain numeric message ids", async () => {
  const dom = new JSDOM('<!doctype html><html><body><div data-invite-slot="42" class="hidden"></div></body></html>', { url: "http://localhost/", runScripts: "outside-only" })
  const w = dom.window
  const urls = []
  w.fetch = (url) => { urls.push(url); return Promise.resolve({ ok: true, status: 204, text: () => Promise.resolve("") }) }
  w.eval(inviteJS)
  for (const id of ["../../settings", "42/../../x", "4 2", "", "-1", "1e3"]) w.postMessage({ type: "emailBodyResize", emailId: id, height: 1 }, "*")
  await new Promise((r) => setTimeout(r, 20))
  assert.deepEqual(urls, [])
  w.postMessage({ type: "emailBodyResize", emailId: "42", height: 1 }, "*")
  await new Promise((r) => setTimeout(r, 20))
  assert.deepEqual(urls, ["/email/42/invite"])
})
