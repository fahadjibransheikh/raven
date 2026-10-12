// The sidebar unread counts must be refetched when the window returns and when the event
// stream (re)connects, since events published while hidden or disconnected are lost.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")
const wait = (ms) => new Promise((r) => setTimeout(r, ms))

async function setup() {
  const dom = new JSDOM('<!doctype html><body><aside><a hx-get="/folder/inbox"><span class="truncate">Inbox</span><span data-folder-unread="inbox">1</span></a></aside></body>',
    { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const sources = []
  w.EventSource = function () { const l = {}; this.listeners = l; this.addEventListener = function (t, fn) { l[t] = fn }; this.close = function () {}; sources.push(this) }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  const calls = []
  let inbox = 1
  w.fetch = function (url) {
    calls.push(url)
    return Promise.resolve({ ok: true, status: 200, json: function () { return Promise.resolve(url === "/api/folders/unread" ? { inbox: inbox } : {}) } })
  }
  w.eval(appJS)
  if (w.document.readyState === "loading") await new Promise((r) => w.document.addEventListener("DOMContentLoaded", r))
  await wait(300)
  return { w, sources, calls, setInbox: (n) => { inbox = n } }
}

const unreadFetches = (calls) => calls.filter((u) => u === "/api/folders/unread").length

test("returning to the window refetches unread counts and clears a stale badge", async () => {
  const t = await setup()
  const before = unreadFetches(t.calls)
  t.setInbox(0)
  Object.defineProperty(t.w.document, "visibilityState", { value: "visible", configurable: true })
  t.w.document.dispatchEvent(new t.w.Event("visibilitychange"))
  await wait(300)
  assert.equal(unreadFetches(t.calls), before + 1)
  assert.equal(t.w.document.querySelector("[data-folder-unread]").style.display, "none")
})

test("the event stream's connected event refetches unread counts", async () => {
  const t = await setup()
  assert.ok(t.sources.length > 0 && t.sources[0].listeners.connected, "listens for connected")
  const before = unreadFetches(t.calls)
  t.sources[0].listeners.connected({ data: "{}" })
  await wait(300)
  assert.equal(unreadFetches(t.calls), before + 1)
})
