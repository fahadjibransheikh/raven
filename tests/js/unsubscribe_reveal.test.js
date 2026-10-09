// A message synced before List-Unsubscribe was captured renders its Unsubscribe button hidden;
// once its body frame reports loaded, the reader asks the server and reveals it in the same view.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function setup(reply) {
  const dom = new JSDOM('<!doctype html><body>' +
    '<iframe data-email-body-frame data-email-id="7" class="opacity-0"></iframe>' +
    '<button class="hidden" data-unsubscribe-id="7" data-unsubscribe-method="" data-unsubscribe-target=""></button></body>',
    { url: "http://localhost/", runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () {} } }) }
  const urls = []
  w.fetch = function (url) {
    urls.push(url)
    return Promise.resolve({ ok: true, status: 200, text: function () { return Promise.resolve("") }, json: function () { return Promise.resolve(reply) } })
  }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  const frame = w.document.querySelector("[data-email-body-frame]")
  const loaded = function () { w.dispatchEvent(new w.MessageEvent("message", { data: { type: "emailBodyResize", height: 50 }, source: frame.contentWindow })) }
  const unsubUrls = function () { return urls.filter(function (u) { return String(u).indexOf("/unsubscribe") >= 0 }) }
  return { w, btn: w.document.querySelector("[data-unsubscribe-id]"), loaded, unsubUrls }
}

test("body loaded -> hidden Unsubscribe button is revealed with the server's method", async () => {
  const { btn, loaded, unsubUrls } = await setup({ method: "mailto", target: "unsub@example.com" })
  assert.ok(btn.classList.contains("hidden"))
  loaded()
  loaded() // the frame may resize several times; ask only once
  await new Promise((r) => setTimeout(r, 20))
  assert.deepEqual(unsubUrls(), ["/api/messages/7/unsubscribe"])
  assert.ok(!btn.classList.contains("hidden"))
  assert.equal(btn.dataset.unsubscribeMethod, "mailto")
  assert.equal(btn.dataset.unsubscribeTarget, "unsub@example.com")
})

test("body loaded -> button stays hidden when the message has no unsubscribe option", async () => {
  const { btn, loaded } = await setup({ method: "", target: "" })
  loaded()
  await new Promise((r) => setTimeout(r, 20))
  assert.ok(btn.classList.contains("hidden"))
})
