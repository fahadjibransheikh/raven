// A thread opens on its target message (data-thread-target), pinned to the top of the
// reading pane, and stays pinned while earlier body frames resize, until the user scrolls.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

// JSDOM has no layout: model it. Items stack in a column; each has a header at
// `top` = sum of earlier heights, and earlier iframes contribute their style.height.
async function setup(targetIdx) {
  const dom = new JSDOM('<!doctype html><body><div id="scroller" class="overflow-y-auto"><div class="mail-view-content">' +
    '<div class="item" id="i1"><iframe data-email-body-frame data-email-id="1"></iframe></div>' +
    '<div class="item" id="i2"><iframe data-email-body-frame data-email-id="2"></iframe></div>' +
    '<div class="item" id="i3"></div></div></div></body>', { url: "http://localhost/", runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return Promise.resolve({ ok: true, status: 204, text: function () { return Promise.resolve("") }, json: function () { return Promise.resolve({}) } }) }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () {} } }) }
  const d = w.document
  const scroller = d.getElementById("scroller")
  scroller.scrollTop = 0 // jsdom's scrollTop is inert; make it a plain property
  Object.defineProperty(scroller, "scrollTop", { value: 0, writable: true })
  Object.defineProperty(scroller, "clientHeight", { value: 600 })
  scroller.getBoundingClientRect = function () { return { top: 100 } }
  const items = ["i1", "i2", "i3"].map(function (id) { return d.getElementById(id) })
  items.forEach(function (el, idx) {
    el.getBoundingClientRect = function () {
      let top = 100 - scroller.scrollTop
      for (let j = 0; j < idx; j++) top += 50 + (parseFloat(items[j].querySelector("iframe").style.height) || 150)
      return { top: top }
    }
  })
  if (targetIdx !== undefined) items[targetIdx].setAttribute("data-thread-target", "")
  w.eval(appJS)
  if (d.readyState === "loading") await new Promise(function (r) { d.addEventListener("DOMContentLoaded", r) })
  const frames = d.querySelectorAll("[data-email-body-frame]")
  const post = function (i, height) { w.dispatchEvent(new w.MessageEvent("message", { data: { type: "emailBodyResize", height: height }, source: frames[i].contentWindow })) }
  return { w, d, scroller, items, post }
}
const tick = function (w) { return new Promise(function (r) { w.setTimeout(r, 5) }) }

test("target header lands at the top and is re-pinned as earlier frames resize", async () => {
  const { w, scroller, items, post } = await setup(2)
  assert.equal(items[2].getBoundingClientRect().top, 100 + 8)
  post(0, 900)
  assert.equal(items[2].getBoundingClientRect().top, 100 + 8)
  post(1, 2000)
  assert.equal(items[2].getBoundingClientRect().top, 100 + 8)
  assert.ok(scroller.scrollTop > 2900)
})

test("once the user scrolls, resizes no longer move the pane", async () => {
  const { w, scroller, post } = await setup(2)
  assert.ok(scroller.scrollTop > 0, "pinned on open")
  scroller.dispatchEvent(new w.Event("wheel"))
  const before = scroller.scrollTop
  post(0, 900)
  assert.equal(scroller.scrollTop, before)
})

test("newest-first (no data-thread-target) never moves the pane", async () => {
  const { w, scroller, post } = await setup()
  await tick(w)
  post(0, 900)
  assert.equal(scroller.scrollTop, 0)
})

test("reply shortcut with no reply bar acts on the newest message of the thread", async () => {
  const { w, d } = await setup()
  const urls = []
  w.fetch = function (u) { urls.push(u); return Promise.reject(new Error("stop")) }
  d.body.insertAdjacentHTML("beforeend",
    '<div data-thread-reply-data data-account-id="a" data-message-id="old"></div>' +
    '<div data-thread-reply-data data-thread-newest data-account-id="a" data-message-id="new"></div>')
  w.handleReply(null, "reply")
  assert.equal(urls.length, 1)
  assert.match(urls[0], /message_id=new$/)
})
