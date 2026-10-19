// The agenda dims events that are over and marks the one under way and the next to start;
// day groups with nothing showing disappear.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

const HOUR = 3600 * 1000
const iso = (ms) => new Date(ms).toISOString()
const pad = (n) => String(n).padStart(2, "0")
const dateKey = (d) => d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate())

function row(id, attrs, extra) {
  return '<button data-calendar-agenda-event data-calendar-source-id="s" data-calendar-source-hidden="false" id="' + id + '" ' + attrs + " " + (extra || "") + "></button>"
}

async function load(t, rowsHTML, { showsToday = true } = {}) {
  const today = dateKey(new Date())
  const html = '<div id="mail-list"><div id="calendar-main" data-calendar-today-date="' + today + '" data-calendar-view="week" data-calendar-period="week:x" data-calendar-month-label="Week">' +
    (showsToday ? '<div data-calendar-day="' + today + '"></div>' : '<div data-calendar-day="2000-01-01"></div>') +
    '</div></div><section data-calendar-agenda><h2 id="calendar-agenda-heading"></h2><span data-calendar-agenda-context></span>' +
    '<button data-calendar-agenda-clear hidden></button><div id="calendar-agenda-list"><div data-calendar-agenda-empty hidden></div>' +
    '<span data-calendar-agenda-empty-title></span><span data-calendar-agenda-empty-detail></span>' +
    '<section data-calendar-agenda-day id="g1"><h3 data-calendar-agenda-day-heading></h3>' + rowsHTML + "</section></div></section>"
  const dom = new JSDOM("<!doctype html><body>" + html + "</body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: false, addEventListener: function () {}, addListener: function () {} } }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () {} } }) }
  w.fetch = function () { return Promise.resolve({ ok: true, status: 200, json: function () { return Promise.resolve({}) }, text: function () { return Promise.resolve("") } }) }
  w.console.warn = function () {}
  t.after(() => w.close()) // stops the minute timer so the test process can exit
  w.eval(appJS)
  return w
}

const timed = (id, startOffset, endOffset, extra) => {
  const now = Date.now()
  return row(id, 'data-calendar-event-all-day="false" data-calendar-event-start="' + iso(now + startOffset) + '" data-calendar-event-end="' + iso(now + endOffset) + '"', extra)
}
const state = (w, id) => w.document.getElementById(id).getAttribute("data-agenda-state")

test("past dims, current is now, first upcoming is next", async (t) => {
  const w = await load(t, 
    timed("over", -3 * HOUR, -2 * HOUR) + timed("current", -HOUR, HOUR) + timed("soon", HOUR, 2 * HOUR) + timed("later", 3 * HOUR, 4 * HOUR))
  w.updateCalendarAgendaTime()
  assert.equal(state(w, "over"), "past")
  assert.equal(state(w, "current"), "now")
  assert.equal(w.document.getElementById("current").getAttribute("aria-current"), "time")
  assert.equal(state(w, "soon"), "next")
  assert.equal(state(w, "later"), null)
})

test("all-day rows are dimmed only once their last day is over", async (t) => {
  const today = dateKey(new Date())
  const w = await load(t, 
    row("ended", 'data-calendar-event-all-day="true" data-calendar-event-start-date="2000-01-01" data-calendar-event-end-date="' + today + '"') +
    row("spans", 'data-calendar-event-all-day="true" data-calendar-event-start-date="2000-01-01" data-calendar-event-end-date="2999-01-01"'))
  w.updateCalendarAgendaTime()
  assert.equal(state(w, "ended"), "past")
  assert.equal(state(w, "spans"), null)
})

test("a hidden calendar's event is never marked next", async (t) => {
  const w = await load(t, timed("hiddenSoon", HOUR, 2 * HOUR).replace('data-calendar-source-hidden="false"', 'data-calendar-source-hidden="true"') + timed("visibleLater", 3 * HOUR, 4 * HOUR))
  w.updateCalendarAgendaTime()
  assert.equal(state(w, "hiddenSoon"), null)
  assert.equal(state(w, "visibleLater"), "next")
})

test("no marks when the view does not contain today", async (t) => {
  const w = await load(t, timed("over", -3 * HOUR, -2 * HOUR) + timed("soon", HOUR, 2 * HOUR), { showsToday: false })
  w.updateCalendarAgendaTime()
  assert.equal(state(w, "over"), null)
  assert.equal(state(w, "soon"), null)
})

test("marks clear when time moves on", async (t) => {
  const w = await load(t, timed("e", -HOUR, HOUR))
  w.updateCalendarAgendaTime()
  assert.equal(state(w, "e"), "now")
  w.document.getElementById("e").dataset.calendarEventEnd = iso(Date.now() - 1000)
  w.updateCalendarAgendaTime()
  assert.equal(state(w, "e"), "past")
  assert.equal(w.document.getElementById("e").hasAttribute("aria-current"), false)
})

test("a day group with every row hidden is hidden too", async (t) => {
  const w = await load(t, timed("only", HOUR, 2 * HOUR, "hidden"))
  w.document.getElementById("only").dataset.calendarSourceHidden = "true"
  w.document.body.dispatchEvent(new w.CustomEvent("htmx:afterSwap"))
  assert.equal(w.document.getElementById("g1").hidden, true)
  w.document.getElementById("only").dataset.calendarSourceHidden = "false"
  w.document.body.dispatchEvent(new w.CustomEvent("htmx:afterSwap"))
  assert.equal(w.document.getElementById("g1").hidden, false)
})
