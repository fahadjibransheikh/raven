// Reply-all must drop every address that means "me" for the account, not just
// its primary address. Runs assets/js/app.js in jsdom with compose From-menu markup.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function loadPage(markup) {
  const dom = new JSDOM("<!doctype html><body>" + markup + "</body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.fetch = function () { return new Promise(function () {}) }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  return w
}

test("reply-all drops the primary address and every identity of the account", async function () {
  const w = await loadPage('<div data-account-id="a1" data-account-email="me@example.com" data-account-self-emails="me@example.com,work@example.com"></div>')
  const vals = w.composeValuesFromSource({
    account_id: "a1",
    from_name: "Friend",
    from_email: "friend@example.com",
    to: "Work@Example.com, other@example.com",
    cc: "me@example.com, third@example.com",
    subject: "Hi"
  }, "reply-all")
  assert.equal(vals.to, "Friend <friend@example.com>, other@example.com")
  assert.equal(vals.cc, "third@example.com")
})

test("reply-all still drops the primary address when no identity list is rendered", async function () {
  const w = await loadPage('<div data-account-id="a1" data-account-email="me@example.com"></div>')
  const vals = w.composeValuesFromSource({ account_id: "a1", from_name: "F", from_email: "f@example.com", to: "me@example.com, o@example.com", cc: "", subject: "Hi" }, "reply-all")
  assert.equal(vals.to, "F <f@example.com>, o@example.com")
})
