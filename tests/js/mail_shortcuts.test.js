// Characterization of the single-key mail shortcuts (key -> what runs). Written against the
// pre-registry handler and kept as the contract the shortcut registry must honour.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function setup() {
  const html = '<aside><div data-sidebar-account="a1"><a hx-get="/folder/f-inbox"><span class="truncate">Inbox</span></a><a hx-get="/folder/f-arch"><span class="truncate">Archive</span></a></div></aside>' +
    '<input data-mail-search-input>' +
    '<div id="mail-list-scroll" data-folder-id="f-inbox">' +
    '<div class="mail-list-item" data-email-id="m1" data-account-id="a1" data-position="0"><a href="#"></a></div>' +
    '<div class="mail-list-item" data-email-id="m2" data-account-id="a1" data-position="1"><a href="#"></a></div>' +
    '<div class="mail-list-item" data-email-id="m3" data-account-id="a1" data-position="2"><a href="#"></a></div></div>' +
    '<div id="outside"></div>'
  const dom = new JSDOM("<!doctype html><body>" + html + "</body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const calls = []
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function (url, o) {
    calls.push({ url: url, body: o && o.body ? JSON.parse(o.body) : null })
    return Promise.resolve({ ok: true, json: function () { return Promise.resolve({ moved: [] }) } })
  }
  w.VirtualMailList = function () { return new Proxy({}, { get: function (_, prop) { return prop === "folderID" ? "f-inbox" : prop === "selectedEmailId" ? null : function () { return Promise.resolve() } } }) }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  const t = { w: w, calls: calls, replies: [], composes: 0, picks: [], opened: [] }
  w.handleReply = function (el, mode) { t.replies.push([el, mode]) }
  w.openNewCompose = function () { t.composes++ }
  w.RavenPalette = { pick: function (label) { t.picks.push(label) } }
  w.document.querySelectorAll(".mail-list-item > a").forEach(function (a) {
    a.addEventListener("click", function (e) { e.preventDefault(); t.opened.push(a.parentNode.dataset.emailId) })
  })
  return t
}
const press = function (w, key, init, target) {
  const ev = new w.KeyboardEvent("keydown", Object.assign({ key: key, bubbles: true, cancelable: true }, init || {}))
  ;(target || w.document.body).dispatchEvent(ev)
  return ev
}
const tick = function () { return new Promise(function (r) { setTimeout(r, 5) }) }
const selected = function (w) {
  return Array.from(w.document.querySelectorAll(".mail-list-item")).filter(function (r) { return r.hasAttribute("data-mail-selected") }).map(function (r) { return r.dataset.emailId })
}
const urls = function (t) { return t.calls.map(function (c) { return c.url }) }
const helpOpen = function (w) { return !!w.document.getElementById("mail-shortcut-help") }
const select = function (t, id) { t.w.document.querySelector('[data-email-id="' + id + '"] > a').click() }

test("j/ArrowDown and k/ArrowUp move the selection and preventDefault", async () => {
  const t = await setup()
  assert.equal(press(t.w, "j").defaultPrevented, true)
  assert.deepEqual(selected(t.w), ["m1"])
  press(t.w, "ArrowDown")
  assert.deepEqual(selected(t.w), ["m2"])
  press(t.w, "k")
  assert.deepEqual(selected(t.w), ["m1"])
  const t2 = await setup()
  press(t2.w, "ArrowUp")
  assert.deepEqual(selected(t2.w), ["m3"])
})

test("Enter and o open the selected row", async () => {
  const t = await setup()
  select(t, "m2"); t.opened.length = 0
  press(t.w, "Enter"); press(t.w, "o")
  assert.deepEqual(t.opened, ["m2", "m2"])
})

test("/ focuses search", async () => {
  const t = await setup()
  press(t.w, "/")
  assert.equal(t.w.document.activeElement, t.w.document.querySelector("[data-mail-search-input]"))
})

test("c composes; r a f call handleReply(null, mode)", async () => {
  const t = await setup()
  press(t.w, "c"); press(t.w, "r"); press(t.w, "a"); press(t.w, "f")
  assert.equal(t.composes, 1)
  assert.deepEqual(t.replies, [[null, "reply"], [null, "reply-all"], [null, "forward"]])
})

test("? and Shift+/ toggle the help overlay; Esc closes it without clearing selection", async () => {
  const t = await setup()
  select(t, "m1")
  press(t.w, "?")
  assert.equal(helpOpen(t.w), true)
  press(t.w, "?")
  assert.equal(helpOpen(t.w), false)
  press(t.w, "/", { shiftKey: true })
  assert.equal(helpOpen(t.w), true)
  const ev = press(t.w, "Escape")
  assert.equal(ev.defaultPrevented, true)
  assert.equal(helpOpen(t.w), false)
  assert.deepEqual(selected(t.w), ["m1"])
})

test("Esc clears the selection, and is left alone when there is none", async () => {
  const t = await setup()
  assert.equal(press(t.w, "Escape").defaultPrevented, false)
  press(t.w, "j")
  assert.equal(press(t.w, "Escape").defaultPrevented, true)
  assert.deepEqual(selected(t.w), [])
})

test("e Delete # s u v act on the selection", async () => {
  const t = await setup()
  select(t, "m1")
  press(t.w, "v")
  await tick()
  assert.equal(t.picks.length, 1)
  for (const [key, url] of [["e", "archive"], ["Delete", "delete"], ["#", "delete"], ["s", "star"], ["u", "m1/read"]]) {
    const one = await setup()
    select(one, "m1")
    press(one.w, key)
    await tick()
    assert.ok(urls(one).some(function (x) { return x.indexOf("/api/messages/" + url) === 0 }), key + " -> " + url + " got " + urls(one))
  }
})

test("selection-only keys do nothing without a selection", async () => {
  const t = await setup()
  ;["e", "Delete", "#", "s", "u", "v"].forEach(function (k) { press(t.w, k) })
  await tick()
  assert.deepEqual(urls(t).filter(function (x) { return x.indexOf("/api/messages/") === 0 }), [])
  assert.deepEqual(t.picks, [])
})

test("keys are ignored with modifiers, in inputs, with compose open, or with a dialog open", async () => {
  const t = await setup()
  press(t.w, "c", { ctrlKey: true }); press(t.w, "c", { metaKey: true }); press(t.w, "c", { altKey: true })
  press(t.w, "c", {}, t.w.document.querySelector("[data-mail-search-input]"))
  assert.equal(t.composes, 0)
  const pane = t.w.document.createElement("div")
  pane.setAttribute("data-compose-pane", "")
  pane.innerHTML = "<span id=cp></span>"
  t.w.document.body.appendChild(pane)
  press(t.w, "c", {}, t.w.document.getElementById("cp"))
  assert.equal(t.composes, 0)
  pane.remove()
  const dlg = t.w.document.createElement("dialog")
  dlg.setAttribute("open", "")
  t.w.document.body.appendChild(dlg)
  press(t.w, "c")
  assert.equal(t.composes, 0)
  dlg.remove()
  press(t.w, "c")
  assert.equal(t.composes, 1)
})
