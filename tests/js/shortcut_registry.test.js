// The shortcut registry (app.js) and the command palette (raven.js) share one table; the Go-side
// toolbar key labels (internal/views/shortcuts.go) must match it.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const root = path.join(__dirname, "..", "..")
const appJS = fs.readFileSync(path.join(root, "assets", "js", "app.js"), "utf8")
const ravenJS = fs.readFileSync(path.join(root, "assets", "js", "raven.js"), "utf8")
const goKeys = fs.readFileSync(path.join(root, "internal", "views", "shortcuts.go"), "utf8")

async function setup(opts) {
  opts = opts || {}
  const sidebar = '<aside>' +
    '<div data-sidebar-account="a1" data-sidebar-account-collapsed="true"><button data-sidebar-account-toggle="a1"><span class="truncate">Work</span></button>' +
    '<a hx-get="/folder/f-inbox"><span class="truncate">Inbox</span></a><a hx-get="/folder/f-arch"><span class="truncate">Archive</span></a></div>' +
    '<a href="/contacts" data-sidebar-app-button="contacts"></a></aside>'
  const rows = '<div id="mail-list-scroll" data-folder-id="f-inbox"><div class="mail-list-item" data-email-id="m1" data-account-id="a1" data-position="0"><a href="#"></a></div></div>' +
    (opts.open ? '<div id="mail-view"><div id="reply-bar"></div></div>' : "")
  const dom = new JSDOM("<!doctype html><body>" + sidebar + rows + "</body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const calls = []
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function (url, o) {
    calls.push({ url: url, body: o && o.body ? JSON.parse(o.body) : null })
    return Promise.resolve({ ok: true, json: function () { return Promise.resolve({}) } })
  }
  w.VirtualMailList = function () { return new Proxy({}, { get: function (_, prop) { return prop === "folderID" ? "f-inbox" : prop === "selectedEmailId" ? null : function () { return Promise.resolve() } } }) }
  w.Element.prototype.scrollIntoView = function () {}
  // jsdom has no modal dialogs
  w.HTMLDialogElement.prototype.showModal = function () { this.setAttribute("open", "") }
  w.HTMLDialogElement.prototype.close = function () { this.removeAttribute("open") }
  w.eval(appJS)
  if (w.document.readyState === "loading") await new Promise(function (r) { w.document.addEventListener("DOMContentLoaded", r) })
  w.eval(ravenJS)
  const t = { w: w, calls: calls, replies: [] }
  w.handleReply = function (el, mode) { t.replies.push([el, mode]) }
  w.document.querySelector("#mail-list-scroll a").addEventListener("click", function (e) { e.preventDefault() })
  return t
}
const labels = function (w) { return Array.from(w.document.querySelectorAll(".raven-palette li[data-index] > span"), function (s) { return s.textContent }) }
const groups = function (w) { return Array.from(w.document.querySelectorAll(".raven-palette .raven-palette-group"), function (s) { return s.textContent }) }
const type = function (w, text) {
  const input = w.document.querySelector(".raven-palette-input")
  input.value = text
  input.dispatchEvent(new w.Event("input"))
  return input
}
const key = function (input, k) { input.dispatchEvent(new input.ownerDocument.defaultView.KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true })) }
const tick = function () { return new Promise(function (r) { setTimeout(r, 5) }) }

test("toolbar chip labels in shortcuts.go match the registry's first key", async () => {
  const t = await setup()
  const pairs = Array.from(goKeys.matchAll(/"([a-z-]+)":\s+"([^"]+)"/g))
  assert.ok(pairs.length >= 8)
  for (const [, id, label] of pairs) {
    const sc = t.w.RavenShortcuts.get(id)
    assert.ok(sc, "registry has " + id)
    assert.equal(sc.keys[0].toUpperCase(), label, id)
  }
})

test("fuzzy filter: subsequence, case-insensitive, prefix and word-start before scattered", async () => {
  const t = await setup()
  const f = t.w.RavenPalette.filter
  const items = [{ label: "Mark read / unread" }, { label: "Archive" }, { label: "Move to…" }, { label: "Toggle light / dark" }, { label: "Reply all" }]
  assert.deepEqual(Array.from(f(items, "arc"), (i) => i.label), ["Archive"])
  assert.deepEqual(Array.from(f(items, "ARCV"), (i) => i.label), ["Archive"])
  assert.deepEqual(Array.from(f(items, "r"), (i) => i.label)[0], "Reply all")
  assert.deepEqual(Array.from(f(items, "all"), (i) => i.label)[0], "Reply all")
  assert.deepEqual(Array.from(f(items, "zzz"), (i) => i.label), [])
  assert.equal(f(items, "").length, items.length)
  // word-start ("unread") beats a scattered match
  assert.equal(f(items, "unr")[0].label, "Mark read / unread")
})

test("message actions are hidden with no message in play and shown once one is selected", async () => {
  const t = await setup()
  t.w.document.dispatchEvent(new t.w.KeyboardEvent("keydown", { key: "k", ctrlKey: true, bubbles: true, cancelable: true }))
  assert.ok(!groups(t.w).includes("Message actions"))
  assert.ok(!labels(t.w).includes("Reply"))
  assert.deepEqual(groups(t.w), ["Go to", "App"])
  t.w.document.querySelector(".raven-palette").close()
  t.w.document.querySelector("#mail-list-scroll a").click()
  t.w.document.dispatchEvent(new t.w.KeyboardEvent("keydown", { key: "k", metaKey: true, bubbles: true, cancelable: true }))
  assert.deepEqual(groups(t.w), ["Message actions", "Go to", "App"])
  for (const l of ["Reply", "Reply all", "Forward", "Archive", "Delete", "Star / unstar", "Mark read / unread", "Move to…"]) assert.ok(labels(t.w).includes(l), l)
  const chips = Array.from(t.w.document.querySelectorAll(".raven-palette kbd.kbd"), (k) => k.textContent)
  assert.ok(chips.includes("E") && chips.includes("#") && chips.includes("R"))
})

test("go to lists folders inside collapsed account sections plus the app pages", async () => {
  const t = await setup()
  t.w.RavenPalette.open()
  const l = labels(t.w)
  for (const want of ["Inbox — Work", "Archive — Work", "Mail", "Contacts", "Calendar", "Settings"]) assert.ok(l.includes(want), want)
  for (const want of ["Compose", "Focus search", "Toggle light / dark", "Keyboard shortcuts", "What's new"]) assert.ok(l.includes(want), want)
})

test("Enter in the palette runs the registry function, identical to the key", async () => {
  const t = await setup({ open: true })
  t.w.RavenPalette.open()
  let input = type(t.w, "reply all")
  key(input, "Enter")
  assert.deepEqual(t.replies, [[null, "reply-all"]])
  assert.equal(t.w.document.querySelector(".raven-palette").open, false)

  t.w.document.querySelector("#mail-list-scroll a").click()
  t.w.RavenPalette.open()
  input = type(t.w, "arch")
  assert.equal(labels(t.w)[0], "Archive")
  key(input, "Enter")
  await tick()
  assert.equal(t.calls.filter((c) => c.url === "/api/messages/archive").length, 1)
})

test("arrow keys move the highlight and Esc is left to the dialog", async () => {
  const t = await setup()
  t.w.RavenPalette.open()
  const input = t.w.document.querySelector(".raven-palette-input")
  const active = () => t.w.document.querySelector(".raven-palette li[data-active] > span").textContent
  const first = active()
  key(input, "ArrowDown")
  assert.notEqual(active(), first)
  key(input, "ArrowUp")
  assert.equal(active(), first)
})
