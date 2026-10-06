// OAuth callbacks redirect to /settings/accounts?error=<code>; the page turns the code into a toast.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function load(search) {
  const dom = new JSDOM("<!doctype html><body></body>", { url: "http://localhost/settings/accounts" + search, runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return Promise.resolve({ ok: true, json: function () { return Promise.resolve({}) } }) }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () {} } }) }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  return w
}

test("oauth_email_mismatch shows a human toast and clears the param", async () => {
  const w = await load("?error=oauth_email_mismatch")
  const toast = w.document.getElementById("account-connection-toast")
  assert.ok(toast)
  assert.match(toast.textContent, /different/i)
  assert.equal(w.location.search, "")
})

test("an unknown error code shows nothing", async () => {
  const w = await load("?error=whatever")
  assert.equal(w.document.getElementById("account-connection-toast"), null)
})
