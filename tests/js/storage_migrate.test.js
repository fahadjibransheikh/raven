// Browser storage written before the rename moves to the new key names once, before anything reads it.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const src = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "storage-migrate.js"), "utf8")

function setup(seed) {
  const dom = new JSDOM("<!doctype html><body></body>", { runScripts: "outside-only", url: "http://localhost/" })
  const w = dom.window
  seed(w)
  w.eval(src)
  return w
}

test("copies legacy localStorage keys to the new names and removes the old ones", () => {
  const w = setup((w) => {
    w.localStorage.setItem("gofer:ui_settings", '{"theme":"dark"}')
    w.localStorage.setItem("gofer_mailto_handler_state", '{"status":"confirmed"}')
    w.localStorage.setItem("unrelated", "keep")
  })
  assert.equal(w.localStorage.getItem("raven:ui_settings"), '{"theme":"dark"}')
  assert.equal(w.localStorage.getItem("raven_mailto_handler_state"), '{"status":"confirmed"}')
  assert.equal(w.localStorage.getItem("gofer:ui_settings"), null)
  assert.equal(w.localStorage.getItem("gofer_mailto_handler_state"), null)
  assert.equal(w.localStorage.getItem("unrelated"), "keep")
  assert.equal(w.localStorage.length, 3)
})

test("sessionStorage is migrated too", () => {
  const w = setup((w) => w.sessionStorage.setItem("gofer_mailto_handler_test_token", "t"))
  assert.equal(w.sessionStorage.getItem("raven_mailto_handler_test_token"), "t")
  assert.equal(w.sessionStorage.getItem("gofer_mailto_handler_test_token"), null)
})

test("a value already stored under the new name wins", () => {
  const w = setup((w) => {
    w.localStorage.setItem("raven:ui_settings", "new")
    w.localStorage.setItem("gofer:ui_settings", "old")
  })
  assert.equal(w.localStorage.getItem("raven:ui_settings"), "new")
  assert.equal(w.localStorage.getItem("gofer:ui_settings"), null)
})

test("running again, or with nothing to migrate, changes nothing", () => {
  const w = setup((w) => w.localStorage.setItem("gofer:ui_settings", "v"))
  w.eval(src)
  assert.equal(w.localStorage.getItem("raven:ui_settings"), "v")
  assert.equal(w.localStorage.length, 1)
})

test("unavailable storage does not throw", () => {
  const dom = new JSDOM("<!doctype html><body></body>", { runScripts: "outside-only", url: "http://localhost/" })
  Object.defineProperty(dom.window, "localStorage", { get() { throw new Error("blocked") } })
  assert.doesNotThrow(() => dom.window.eval(src))
})
