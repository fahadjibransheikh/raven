// Notifications go through window.__TAURI__.notification when the Tauri bridge
// exists (WKWebView has no web permission prompt) and through the web
// Notification API otherwise. Runs assets/js/app.js in jsdom.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function loadPage(setup) {
  const dom = new JSDOM("<!doctype html><body><span data-desktop-notifications-status></span></body>", { url: "http://127.0.0.1:8090/", runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.fetch = function () { return new Promise(function () {}) }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  setup(w)
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  return w
}

function stubTauri(w, state) {
  state.sent = []
  w.__TAURI__ = {
    notification: {
      isPermissionGranted: function () { return Promise.resolve(state.granted) },
      requestPermission: function () { state.requested = true; return Promise.resolve(state.result) },
      sendNotification: function (o) { state.sent.push(o) },
    },
  }
}

function stubWebNotification(w, state) {
  state.created = []
  state.asked = false
  w.Notification = function (title, opts) { state.created.push({ title: title, opts: opts }); this.close = function () {} }
  w.Notification.permission = "default"
  w.Notification.requestPermission = function () { state.asked = true; w.Notification.permission = "granted"; return Promise.resolve("granted") }
}

test("with the Tauri bridge, permission and delivery use the plugin, not the web API", async function () {
  const t = { granted: false, result: "granted" }
  const web = {}
  const w = await loadPage(function (w) { stubTauri(w, t); stubWebNotification(w, web) })
  assert.equal(await w.goferNotifications.request(true), "granted")
  assert.equal(t.requested, true)
  assert.equal(web.asked, false)
  t.sent.length = 0
  w.goferNotifications.deliver("Alice", { body: "Hello" })
  assert.equal(t.sent.length, 1)
  assert.equal(t.sent[0].title, "Alice")
  assert.equal(t.sent[0].body, "Hello")
  assert.equal(web.created.length, 0)
})

test("with the Tauri bridge, a denied plugin state is reported with the System Settings hint", async function () {
  const t = { granted: false, result: "denied" }
  const w = await loadPage(function (w) { stubTauri(w, t) })
  assert.equal(await w.goferNotifications.request(true), "denied")
  assert.equal(t.sent.length, 0)
})

test("without the Tauri bridge, the web Notification API is used", async function () {
  const web = {}
  const w = await loadPage(function (w) { stubWebNotification(w, web) })
  assert.equal(await w.goferNotifications.request(true), "granted")
  assert.equal(web.asked, true)
  w.goferNotifications.deliver("Bob", { body: "Hi", tag: "x" })
  assert.equal(web.created.length, 1)
  assert.equal(web.created[0].title, "Bob")
})

test("a partial __TAURI__ object without the notification plugin falls back to the web API", async function () {
  const web = {}
  const w = await loadPage(function (w) { w.__TAURI__ = { core: {} }; stubWebNotification(w, web) })
  w.goferNotifications.deliver("Carol", { body: "Yo" })
  assert.equal(web.created.length, 1)
})
