// A calendar-reminder event on the SSE stream becomes a notification through the
// active browser-tab/desktop path, and clicking it follows the notification's url.
// Web Push is delivered by the server, so the page must not show a second copy.
const { test, after } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")
const wait = (ms) => new Promise((r) => setTimeout(r, ms))
// Reminder cards keep a dismiss timer; closing the windows clears it so the run can exit.
const windows = []
after(() => windows.forEach((w) => w.close()))

async function setup(settings, permission) {
  const dom = new JSDOM("<!doctype html><body></body>", { url: "http://127.0.0.1:8090/", runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  windows.push(w)
  const sources = []
  w.EventSource = function () { const l = {}; this.listeners = l; this.addEventListener = function (t, fn) { l[t] = fn }; this.close = function () {}; sources.push(this) }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return new Promise(function () {}) }
  w.RavenSettings = { get: function (k) { return settings[k] }, set: function () {} }
  const created = []
  w.Notification = function (title, opts) { this.data = opts.data; this.close = function () {}; created.push({ title, opts, self: this }) }
  w.Notification.permission = permission
  w.Notification.requestPermission = function () { return Promise.resolve(permission) }
  w.eval(appJS)
  if (w.document.readyState === "loading") await new Promise((r) => w.document.addEventListener("DOMContentLoaded", r))
  await wait(50)
  return { w, created, fire: (data) => sources[0].listeners["calendar-reminder"]({ data: JSON.stringify(data) }) }
}

const reminder = { event_id: "e1", title: "Roadmap review", body: "Starts in 5 minutes · Room 4", url: "/calendar" }
const browserTab = { desktop_notifications: "true", notification_mode: "browser_tab" }

test("a reminder shows a notification carrying the title, time and calendar url", async () => {
  const t = await setup(browserTab, "granted")
  t.fire(reminder)
  assert.equal(t.created.length, 1)
  assert.equal(t.created[0].title, "Roadmap review")
  assert.equal(t.created[0].opts.body, "Starts in 5 minutes · Room 4")
  assert.equal(t.created[0].opts.tag, "raven-calendar-e1")
  assert.equal(t.created[0].opts.data.url, "/calendar")
})

test("clicking a notification that carries a url goes there", async () => {
  const t = await setup(browserTab, "granted")
  t.w.focus = function () {}
  // A hash url is navigable in jsdom, which cannot load real pages.
  t.w.ravenNotifications.deliver("Meeting", { body: "soon", data: { url: "#reminder" } })
  t.created[0].self.onclick()
  assert.equal(t.w.location.hash, "#reminder")
})

test("no notification when notifications are off, unpermitted, or Web Push delivers them", async () => {
  for (const [settings, permission] of [
    [{ desktop_notifications: "false", notification_mode: "browser_tab" }, "granted"],
    [{ desktop_notifications: "true", notification_mode: "off" }, "granted"],
    [browserTab, "denied"],
    [{ desktop_notifications: "true", notification_mode: "web_push" }, "granted"],
  ]) {
    const t = await setup(settings, permission)
    t.fire(reminder)
    assert.equal(t.created.length, 0, JSON.stringify(settings) + " " + permission)
  }
})

test("a reminder without a title is ignored and an off-site url falls back to the calendar", async () => {
  const t = await setup(browserTab, "granted")
  t.fire({ event_id: "e2" })
  assert.equal(t.created.length, 0)
  t.fire({ ...reminder, url: "//evil.example/x" })
  assert.equal(t.created[0].opts.data.url, "/calendar")
})

test("a reminder shows a card that stays, with Join when the meeting has a link", async () => {
  const t = await setup({ desktop_notifications: "false" }, "default")
  t.fire({ ...reminder, start_at: new Date(Date.now() + 5 * 60000).toISOString(), join_url: "https://meet.google.com/abc-defg-hij" })
  const card = t.w.document.getElementById("calendar-reminder-e1")
  assert.ok(card, "card shown even with desktop notifications off")
  assert.equal(card.dataset.tuiToastDuration, "0")
  const join = card.querySelector("[data-raven-toast-link]")
  assert.equal(join.getAttribute("href"), "https://meet.google.com/abc-defg-hij")
  assert.equal(join.getAttribute("target"), "_blank")
  assert.equal(join.textContent, "Join meeting")
})

test("a card without a safe link has no Join, and a past meeting shows no card", async () => {
  const t = await setup({}, "default")
  t.fire({ ...reminder, event_id: "e3", join_url: "javascript:alert(1)" })
  assert.equal(t.w.document.querySelector("#calendar-reminder-e3 [data-raven-toast-link]"), null)
  t.fire({ ...reminder, event_id: "e4", start_at: new Date(Date.now() - 20 * 60000).toISOString() })
  assert.equal(t.w.document.getElementById("calendar-reminder-e4"), null)
})

test("the browser notification stays until dismissed and clicking it opens the meeting", async () => {
  const t = await setup(browserTab, "granted")
  const opened = []
  t.w.open = function (url) { opened.push(url) }
  t.fire({ ...reminder, join_url: "https://meet.google.com/abc-defg-hij" })
  assert.equal(t.created[0].opts.requireInteraction, true)
  t.created[0].self.onclick()
  assert.deepEqual(opened, ["https://meet.google.com/abc-defg-hij"])
})
