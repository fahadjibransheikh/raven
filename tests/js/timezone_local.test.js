// A "local" timezone setting is never frozen into a concrete zone and persisted;
// init publishes the device zone in a cookie so the server can resolve "local"
// per request (a traveller's list dates follow the device).
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const uiSettings = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "ui-settings.js"), "utf8")

function init(timezone) {
  const dom = new JSDOM("<!doctype html><html><body data-ui-settings='" + JSON.stringify({ timezone }) + "'></body></html>", { url: "http://localhost/", runScripts: "outside-only" })
  const w = dom.window
  const fetches = []
  w.fetch = (url, opts) => {
    fetches.push({ url, opts })
    return Promise.resolve({ json: () => Promise.resolve({}) })
  }
  w.eval(uiSettings)
  w.GoferSettings.init()
  return { w, fetches }
}

test("local stays local, nothing is persisted, and the cookie carries the device zone", () => {
  const { w, fetches } = init("local")
  assert.equal(w.GoferSettings.get("timezone"), "local")
  assert.deepEqual(fetches.filter((f) => f.opts && f.opts.method === "PATCH"), [])
  const zone = w.Intl.DateTimeFormat().resolvedOptions().timeZone
  assert.ok(w.document.cookie.includes("raven_tz=" + encodeURIComponent(zone)), w.document.cookie)
})

test("an explicit zone is kept as is", () => {
  const { w, fetches } = init("Asia/Karachi")
  assert.equal(w.GoferSettings.get("timezone"), "Asia/Karachi")
  assert.deepEqual(fetches.filter((f) => f.opts && f.opts.method === "PATCH"), [])
})
