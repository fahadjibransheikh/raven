// "?" opens the shortcut overlay on the calendar page too, listing the calendar keys; the mail
// shortcuts stay off there, and on mail pages the calendar keys are listed after the mail ones.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const root = path.join(__dirname, "..", "..")
const appJS = fs.readFileSync(path.join(root, "assets", "js", "app.js"), "utf8")
const calendarKeysJS = fs.readFileSync(path.join(root, "assets", "js", "calendar-keys.js"), "utf8")

const MAIL_LIST = '<div id="mail-list-scroll" data-folder-id="f-inbox"><div class="mail-list-item" data-email-id="m1" data-account-id="a1" data-position="0"><a href="#"></a></div></div>'
const CALENDAR = '<div id="calendar-main"></div><button data-calendar-key="today"></button>'

async function setup(page) {
  const dom = new JSDOM("<!doctype html><body>" + page + "</body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return Promise.resolve({ ok: true, json: function () { return Promise.resolve({}) } }) }
  w.VirtualMailList = function () { return new Proxy({}, { get: function (_, prop) { return prop === "folderID" ? "f-inbox" : prop === "selectedEmailId" ? null : function () { return Promise.resolve() } } }) }
  w.Element.prototype.scrollIntoView = function () {}
  w.eval(appJS)
  if (w.document.readyState === "loading") await new Promise(function (r) { w.document.addEventListener("DOMContentLoaded", r) })
  w.eval(calendarKeysJS)
  return w
}

const press = function (w, key, opts) {
  const ev = new w.KeyboardEvent("keydown", Object.assign({ key: key, bubbles: true, cancelable: true }, opts))
  w.document.body.dispatchEvent(ev)
  return ev
}
const help = function (w) { return w.document.getElementById("mail-shortcut-help") }
const labels = function (w) { return Array.from(help(w).querySelectorAll("span.text-foreground"), function (s) { return s.textContent }) }

test("? opens the overlay on the calendar page with the calendar keys only", async () => {
  const w = await setup(CALENDAR)
  assert.equal(help(w), null)
  const ev = press(w, "?", { shiftKey: true })
  assert.ok(help(w), "overlay should open")
  assert.equal(ev.defaultPrevented, true)
  assert.deepEqual(labels(w), ["Today", "Next", "Previous", "Month", "Week", "New event", "Edit open event", "Save event", "Close"])
  assert.equal(help(w).querySelector("[data-shortcut-help-section]").textContent, "Calendar")
  const keys = Array.from(help(w).querySelectorAll("kbd"), function (k) { return k.textContent })
  for (const want of ["t", "j", "→", "k", "←", "m", "w", "c", "e", "⌘/Ctrl", "Enter", "Esc"]) assert.ok(keys.includes(want), want)
})

test("? again, and Esc, close the overlay on the calendar page", async () => {
  const w = await setup(CALENDAR)
  press(w, "?", { shiftKey: true })
  press(w, "?", { shiftKey: true })
  assert.equal(help(w), null)
  press(w, "?", { shiftKey: true })
  press(w, "Escape")
  assert.equal(help(w), null)
})

test("mail shortcuts stay off on the calendar page, and calendar keys wait while the overlay is up", async () => {
  const w = await setup(CALENDAR + MAIL_LIST)
  const clicks = []
  w.document.addEventListener("click", function (e) { clicks.push(e.target.getAttribute("data-calendar-key")) })
  for (const k of ["j", "k", "c", "e", "s", "r", "u"]) assert.equal(press(w, k).defaultPrevented, false, "mail key " + k + " must not act on the calendar page")
  assert.equal(help(w), null)
  clicks.length = 0
  press(w, "t")
  assert.deepEqual(clicks, ["today"])
  press(w, "?", { shiftKey: true })
  clicks.length = 0
  press(w, "t")
  assert.deepEqual(clicks, [], "calendar keys must not act behind the overlay")
})

test("on a mail page the overlay lists the mail keys, then the calendar keys", async () => {
  const w = await setup(MAIL_LIST)
  press(w, "?", { shiftKey: true })
  const all = labels(w)
  assert.ok(all.includes("Archive") && all.includes("Compose"))
  assert.deepEqual(all.slice(-9), ["Today", "Next", "Previous", "Month", "Week", "New event", "Edit open event", "Save event", "Close"])
  assert.equal(help(w).querySelector("[data-shortcut-help-section]").textContent, "Calendar page")
})
