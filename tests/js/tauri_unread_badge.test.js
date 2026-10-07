// The desktop badge script learns the inbox count from the page's own
// /api/folders/unread fetches (triggered by SSE) instead of polling every 30s.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const script = fs.readFileSync(path.join(__dirname, "..", "..", "tauri-wrapper", "src-tauri", "src", "unread_badge.js"), "utf8")

function load(url) {
  const dom = new JSDOM("<!doctype html><head><title>Raven</title></head><body></body>", { url, runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const calls = []
  let inbox = 0
  w.fetch = function (input) {
    calls.push(String(input))
    return Promise.resolve({ ok: true, clone: function () { return this }, json: function () { return Promise.resolve({ inbox: inbox }) } })
  }
  const intervals = []
  w.setInterval = function (fn, ms) { intervals.push(ms); return 1 }
  w.eval(script)
  return { w, calls, intervals, setInbox: function (n) { inbox = n } }
}

const tick = () => new Promise(function (resolve) { setTimeout(resolve, 10) })

test("ignores non-Gofer origins", () => {
  const { w, calls } = load("file:///placeholder/index.html")
  assert.equal(calls.length, 0)
  assert.equal(w.document.title, "Raven")
})

test("page fetches of the unread counts update the title without extra polling", async () => {
  const { w, calls, intervals, setInbox } = load("http://127.0.0.1:8090/")
  await tick()
  assert.equal(calls.length, 1, "one initial poll")
  assert.deepEqual(intervals, [5 * 60 * 1000], "fallback poll is slow")

  setInbox(4)
  await w.fetch("/api/folders/unread") // what app.js does after a new-mail SSE event
  await tick()
  assert.equal(w.document.title, "(4) Raven")
  assert.equal(calls.length, 2, "the page's own fetch is the only extra request")

  setInbox(0)
  await w.fetch("/api/folders/unread")
  await tick()
  assert.equal(w.document.title, "Raven")
})
