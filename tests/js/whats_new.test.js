// What's New window: shows when the server says so, persists the version on every way of dismissing it.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const src = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "whats-new.js"), "utf8")

const FRAGMENT = '<div id="whats-new-dialog" data-tui-dialog><dialog data-tui-dialog-content aria-labelledby="whats-new-title">' +
  '<h2 id="whats-new-title">What\'s new</h2><button data-tui-dialog-close autofocus>Got it</button></dialog></div>'

async function setup(response) {
  const dom = new JSDOM("<!doctype html><body></body>", { runScripts: "outside-only", url: "http://localhost/" })
  const w = dom.window
  const calls = []
  w.fetch = function (url, opts) {
    calls.push({ url: url, opts: opts })
    if (String(url).indexOf("/api/whats-new") === 0) {
      return Promise.resolve({ ok: true, json: function () { return Promise.resolve(response) } })
    }
    return Promise.resolve({ ok: true })
  }
  // jsdom has no <dialog> methods; the real dialog.js is covered elsewhere, so model open/close/click-away/Esc.
  w.HTMLDialogElement.prototype.showModal = function () { this.setAttribute("open", "") }
  w.tui = { dialog: {
    open: function (id) { w.document.getElementById(id).querySelector("dialog").showModal() },
    close: function (id) { const d = w.document.getElementById(id).querySelector("dialog"); d.removeAttribute("open"); d.dispatchEvent(new w.Event("close")) },
  } }
  w.document.addEventListener("click", function (e) {
    if (e.target.closest("[data-tui-dialog-close]")) w.tui.dialog.close("whats-new-dialog")
  })
  w.eval(src)
  await new Promise(function (r) { setTimeout(r, 700) })
  return { w: w, calls: calls, get patches() { return calls.filter(function (c) { return c.opts && c.opts.method === "PATCH" }) } }
}

const show = { action: "show", version: "0.1.13", html: FRAGMENT }

test("shows the dialog when the server returns notes", async () => {
  const t = await setup(show)
  const dlg = t.w.document.querySelector("#whats-new-dialog dialog")
  assert.ok(dlg && dlg.hasAttribute("open"))
  assert.equal(t.patches.length, 0, "nothing is stored until it is dismissed")
})

test("Got it dismisses and stores the seen version", async () => {
  const t = await setup(show)
  t.w.document.querySelector("[data-tui-dialog-close]").click()
  assert.equal(t.w.document.getElementById("whats-new-dialog"), null)
  assert.equal(t.patches.length, 1)
  assert.equal(t.patches[0].url, "/api/settings/ui")
  assert.deepEqual(JSON.parse(t.patches[0].opts.body), { whats_new_seen: "0.1.13" })
})

test("Esc and click-away close through the same close event, so they store it too", async () => {
  const t = await setup(show)
  t.w.document.querySelector("#whats-new-dialog dialog").dispatchEvent(new t.w.Event("close"))
  assert.equal(t.patches.length, 1)
  assert.equal(JSON.parse(t.patches[0].opts.body).whats_new_seen, "0.1.13")
})

test("also updates the settings cache so a later full save cannot erase it", async () => {
  const sets = []
  const dom = await setup(show)
  dom.w.GoferSettings = { set: function (k, v) { sets.push([k, v]) } }
  dom.w.document.querySelector("[data-tui-dialog-close]").click()
  assert.deepEqual(sets, [["whats_new_seen", "0.1.13"]])
})

test("nothing to show: no dialog, and no store for action none", async () => {
  const t = await setup({ action: "none", version: "0.1.13" })
  assert.equal(t.w.document.getElementById("whats-new-dialog"), null)
  assert.equal(t.patches.length, 0)
})

test("first run: the version is recorded silently with no dialog", async () => {
  const t = await setup({ action: "record", version: "0.1.13" })
  assert.equal(t.w.document.getElementById("whats-new-dialog"), null)
  assert.equal(t.patches.length, 1)
})

test("the menu entry reopens it on demand", async () => {
  const t = await setup({ action: "none", version: "0.1.13" })
  t.w.document.body.innerHTML = "<button data-whats-new-open></button>"
  t.w.fetch = function (url) {
    t.calls.push({ url: url })
    return Promise.resolve({ ok: true, json: function () { return Promise.resolve(show) } })
  }
  t.w.document.querySelector("[data-whats-new-open]").click()
  await new Promise(function (r) { setTimeout(r, 20) })
  assert.ok(t.calls.some(function (c) { return c.url === "/api/whats-new?mode=open" }))
  assert.ok(t.w.document.querySelector("#whats-new-dialog dialog").hasAttribute("open"))
})
