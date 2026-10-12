// Swipe gestures on mail rows go through the same performMailAction path as the hover quick actions.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")
const WIDTH = 300

const UNREAD = '<span data-mail-card-field="unread" class="inline-flex size-4"><span class="size-2 rounded-full bg-primary"></span></span>'
const READ = '<span data-mail-card-field="unread" class="mail-list-card-empty-icon-slot" aria-hidden="true"></span>'

async function setup(opts) {
  opts = opts || {}
  const row = '<div class="mail-list-item" data-email-id="m1" data-account-id="a"><a href="#" hx-get="/x?folder_id=f-inbox">' + (opts.read ? READ : UNREAD) + "</a></div>"
  const dom = new JSDOM('<!doctype html><body><button data-mail-selection-clear class="hidden"></button><div id="mail-list-scroll" data-folder-id="f-inbox">' + row + "</div></body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const calls = []
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  // Every media query matches: phone/coarse pointer on, and reduced motion on so snap-back is immediate.
  w.matchMedia = function () { return { matches: opts.touch !== false, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function (url, init) {
    calls.push({ url: url, body: init && init.body ? JSON.parse(init.body) : null })
    return Promise.resolve({ ok: true, json: function () { return Promise.resolve({ updated: 1, moved: [{ id: "m1", thread: false, from: "f-inbox" }] }) } })
  }
  w.VirtualMailList = function () { return new Proxy({}, { get: function (_, prop) { return prop === "folderID" ? "f-inbox" : function () { return Promise.resolve() } } }) }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  const el = w.document.querySelector(".mail-list-item")
  // Ctrl-click ticks the row (the app's multi-select), which un-hides the Clear button.
  if (opts.selecting) el.querySelector("a").dispatchEvent(new w.MouseEvent("click", { ctrlKey: true, bubbles: true, cancelable: true }))
  el.getBoundingClientRect = function () { return { width: WIDTH, height: 64, left: 0, top: 0, right: WIDTH, bottom: 64 } }
  return { w: w, calls: calls, row: el, clicks: [] }
}

const tick = function () { return new Promise(function (r) { setTimeout(r, 5) }) }

function fire(t, type, x, y, target) {
  const ev = new t.w.PointerEvent(type, { clientX: x, clientY: y, pointerId: 1, pointerType: "touch", bubbles: true, cancelable: true })
  ;(target || t.row.querySelector("a")).dispatchEvent(ev)
}

// Press at the row middle, move in steps to (dx, dy), and optionally lift.
function drag(t, dx, dy, end) {
  const x0 = 150
  const y0 = 30
  fire(t, "pointerdown", x0, y0)
  for (let i = 1; i <= 5; i++) fire(t, "pointermove", x0 + (dx * i) / 5, y0 + (dy * i) / 5)
  if (end !== false) fire(t, end || "pointerup", x0 + dx, y0 + dy)
}

const actionCalls = function (t) { return t.calls.filter(function (c) { return /^\/api\/messages\//.test(c.url) }) }

test("swipe right past 30% on an unread row marks it read", async () => {
  const t = await setup()
  drag(t, WIDTH * 0.4, 2)
  await tick()
  const calls = actionCalls(t)
  assert.deepEqual(calls.map(function (c) { return c.url }), ["/api/messages/read"])
  assert.equal(calls[0].body.state, undefined)
  assert.equal(calls[0].body.targets[0].id, "m1")
})

test("swipe right on a read row marks it unread", async () => {
  const t = await setup({ read: true })
  drag(t, WIDTH * 0.4, 0)
  await tick()
  const calls = actionCalls(t)
  assert.deepEqual(calls.map(function (c) { return c.url }), ["/api/messages/read"])
  assert.equal(calls[0].body.state, "unread")
})

test("swipe left past 30% archives, with the shared undo toast", async () => {
  const t = await setup()
  drag(t, -WIDTH * 0.45, -3)
  await tick()
  assert.deepEqual(actionCalls(t).map(function (c) { return c.url }), ["/api/messages/archive"])
  assert.equal(t.row.style.display, "none", "row removed optimistically like the hover action")
  assert.match(t.w.document.getElementById("mail-undo-toast").textContent, /Archived/)
})

test("swipe left past 65% marks read and archives", async () => {
  const t = await setup()
  drag(t, -WIDTH * 0.8, 0)
  await tick()
  assert.deepEqual(actionCalls(t).map(function (c) { return c.url }), ["/api/messages/read", "/api/messages/archive"])
})

test("the reveal switches label when crossing the long-swipe threshold", async () => {
  const t = await setup()
  drag(t, -WIDTH * 0.4, 0, false)
  const reveal = t.row.querySelector(".mail-swipe-reveal")
  assert.match(reveal.textContent, /^Archive$/)
  assert.equal(reveal.getAttribute("data-armed"), "true")
  assert.equal(t.row.style.getPropertyValue("--swipe-x"), -WIDTH * 0.4 + "px")
  fire(t, "pointermove", 150 - WIDTH * 0.7, 30)
  assert.match(reveal.textContent, /Read \+ Archive/)
  fire(t, "pointercancel", 0, 0)
  assert.equal(t.row.querySelector(".mail-swipe-reveal"), null)
  assert.equal(t.row.hasAttribute("data-swiping"), false)
  assert.equal(actionCalls(t).length, 0)
})

test("a mostly vertical drag triggers nothing and never starts a swipe", async () => {
  const t = await setup()
  drag(t, WIDTH * 0.5, 120)
  await tick()
  assert.equal(actionCalls(t).length, 0)
  assert.equal(t.row.hasAttribute("data-swiping"), false)
  // Horizontal that does not dominate vertical (under 1.5x) stays unlocked too.
  drag(t, 60, 50)
  await tick()
  assert.equal(actionCalls(t).length, 0)
})

test("a short drag snaps back with no action", async () => {
  const t = await setup()
  drag(t, WIDTH * 0.2, 0)
  await tick()
  assert.equal(actionCalls(t).length, 0)
  assert.equal(t.row.hasAttribute("data-swiping"), false)
  assert.equal(t.row.style.getPropertyValue("--swipe-x"), "")
  assert.equal(t.row.querySelector(".mail-swipe-reveal"), null)
  assert.equal(t.row.style.display, "")
})

test("a tap does nothing; a swipe does not click through to the message", async () => {
  const t = await setup()
  let clicks = 0
  t.row.querySelector("a").addEventListener("click", function () { clicks++ })
  fire(t, "pointerdown", 150, 30)
  fire(t, "pointerup", 150, 30)
  t.row.querySelector("a").click()
  assert.equal(clicks, 1, "tap still opens")
  assert.equal(actionCalls(t).length, 0)

  drag(t, WIDTH * 0.2, 0) // swipe that snaps back, then the browser's trailing click
  t.row.querySelector("a").click()
  assert.equal(clicks, 1, "trailing click after a swipe is swallowed")
})

test("inactive without the phone layout or a coarse pointer, and while rows are multi-selected", async () => {
  const desktop = await setup({ touch: false })
  drag(desktop, -WIDTH * 0.5, 0)
  await tick()
  assert.equal(actionCalls(desktop).length, 0)

  const selecting = await setup({ selecting: true })
  drag(selecting, -WIDTH * 0.5, 0)
  await tick()
  assert.equal(actionCalls(selecting).length, 0)
})

test("a mouse drag is ignored", async () => {
  const t = await setup()
  const ev = function (type, x) { return new t.w.PointerEvent(type, { clientX: x, clientY: 30, pointerId: 1, pointerType: "mouse", bubbles: true }) }
  const a = t.row.querySelector("a")
  a.dispatchEvent(ev("pointerdown", 150))
  a.dispatchEvent(ev("pointermove", 50))
  a.dispatchEvent(ev("pointerup", 50))
  await tick()
  assert.equal(actionCalls(t).length, 0)
  assert.equal(t.row.hasAttribute("data-swiping"), false)
})
