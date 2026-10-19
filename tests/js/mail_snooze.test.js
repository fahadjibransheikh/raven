// Snooze presets resolve in local time; snoozing posts the time and offers Undo.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function load() {
  const dom = new JSDOM('<!doctype html><body><div id="mail-list-scroll"></div></body>', { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const calls = []
  const warnings = []
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () {} } }) }
  w.console.warn = function () { warnings.push(Array.from(arguments).join(" ")) }
  w.fetch = function (url, opts) {
    calls.push({ url: url, method: (opts && opts.method) || "GET", body: opts && opts.body })
    return Promise.resolve({ ok: true, status: 200, json: function () { return Promise.resolve({ cleared: true }) } })
  }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  return { w, calls, warnings }
}

const tick = function () { return new Promise(function (r) { setTimeout(r, 20) }) }

test("presets land on the next matching local day at 08:00", async () => {
  const { w } = await load()
  const wed = new w.Date(2026, 9, 21, 14, 30) // Wednesday 21 Oct 2026, 14:30 local
  const at = function (preset) { const t = w.snoozePresetTime(preset, wed); return [t.getDay(), t.getDate(), t.getHours(), t.getMinutes()] }
  assert.deepEqual(at("later"), [3, 21, 17, 0])
  assert.deepEqual(at("tomorrow"), [4, 22, 8, 0])
  assert.deepEqual(at("weekend"), [6, 24, 8, 0])
  assert.deepEqual(at("next-week"), [1, 26, 8, 0])
  const sat = new w.Date(2026, 9, 24, 9, 0)
  assert.equal(w.snoozePresetTime("weekend", sat).getDate(), 31, "on a Saturday, this weekend means next Saturday")
  w.close()
})

test("custom input must be a future local time", async () => {
  const { w } = await load()
  assert.equal(w.parseSnoozeInput("2020-01-01 08:00"), null)
  assert.equal(w.parseSnoozeInput("tomorrow"), null)
  const t = w.parseSnoozeInput("2099-03-04 17:45")
  assert.deepEqual([t.getFullYear(), t.getMonth(), t.getDate(), t.getHours(), t.getMinutes()], [2099, 2, 4, 17, 45])
  assert.equal(w.parseSnoozeInput("2099-03-04").getHours(), 8)
  w.close()
})

test("snoozing posts the time and Undo clears it", async () => {
  const { w, calls, warnings } = await load()
  await w.snoozeThread("42", "tomorrow")
  await tick()
  assert.deepEqual(warnings, [])
  const post = calls.find(function (c) { return c.method === "POST" })
  assert.equal(post.url, "/api/messages/42/snooze")
  const until = new Date(JSON.parse(post.body).until)
  assert.ok(until > new Date(), "snooze time is in the future")
  assert.match(JSON.parse(post.body).until, /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/)
  const undo = w.document.querySelector("[data-raven-toast-secondary]")
  assert.ok(undo, "undo offered")
  undo.click()
  await tick()
  assert.ok(calls.some(function (c) { return c.method === "DELETE" && c.url === "/api/messages/42/snooze" }))
  w.close()
})

test("b is registered as the snooze shortcut", async () => {
  const { w } = await load()
  const entry = w.RavenShortcuts.get("snooze")
  assert.ok(entry)
  assert.equal(JSON.stringify(entry.match), '["b"]')
  w.close()
})
