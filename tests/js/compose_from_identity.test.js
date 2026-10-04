// The compose From picker lists identities; account_id and from_email must
// always agree, and reopening a draft must reselect the identity it was saved with.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

const MARKUP =
  '<div id="compose-form">' +
  '<input type="hidden" name="account_id" id="compose-account-id" value="a1">' +
  '<input type="hidden" name="from_email" id="compose-from-email" value="me@example.com">' +
  '<span id="compose-from-display">Work &lt;me@example.com&gt;</span></div>' +
  item("a1", "me@example.com", "Work", true, true) +
  item("a1", "sales@example.com", "Sales", false, false) +
  item("a2", "home@example.com", "Home", true, false)

function item(account, email, name, isDefault, selected) {
  return '<div data-compose-account-item data-compose-account-scope="dialog" data-account-id="' + account +
    '" data-account-email="' + email + '" data-account-name="' + name + '" data-identity-default="' + isDefault +
    '" data-compose-account-selected="' + selected + '"></div>'
}

async function loadPage() {
  const dom = new JSDOM("<!doctype html><body>" + MARKUP + "</body>", { runScripts: "outside-only", pretendToBeVisual: true })
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

function state(w) {
  const selected = Array.prototype.map.call(
    w.document.querySelectorAll('[data-compose-account-selected="true"]'),
    function (el) { return el.dataset.accountEmail })
  return {
    account: w.document.getElementById("compose-account-id").value,
    from: w.document.getElementById("compose-from-email").value,
    display: w.document.getElementById("compose-from-display").textContent,
    selected: selected
  }
}

test("reopening a draft reselects the identity it was saved with", async function () {
  const w = await loadPage()
  const vals = w._composeValsFromDraft({ account_id: "a1", from_email: "sales@example.com", draft_id: "d1" })
  assert.equal(vals.from_email, "sales@example.com")
  w.setComposeAccount(w.document.getElementById("compose-form"), vals.account_id, vals.from_email)
  assert.deepEqual(state(w), { account: "a1", from: "sales@example.com", display: "Sales <sales@example.com>", selected: ["sales@example.com"] })
})

test("an unknown or stale from_email falls back to the account default", async function () {
  const w = await loadPage()
  const form = w.document.getElementById("compose-form")
  w.setComposeAccount(form, "a1", "removed@example.com")
  assert.equal(state(w).from, "me@example.com")
  // an identity of another account must not leak across accounts
  w.setComposeAccount(form, "a2", "sales@example.com")
  assert.deepEqual(state(w), { account: "a2", from: "home@example.com", display: "Home <home@example.com>", selected: ["home@example.com"] })
})

test("picking an item sets account_id and from_email together", async function () {
  const w = await loadPage()
  w.selectComposeAccount(w.document.querySelector('[data-account-email="sales@example.com"]'))
  assert.deepEqual(state(w), { account: "a1", from: "sales@example.com", display: "Sales <sales@example.com>", selected: ["sales@example.com"] })
  w.selectComposeAccount(w.document.querySelector('[data-account-email="home@example.com"]'))
  assert.equal(state(w).account, "a2")
  assert.equal(state(w).from, "home@example.com")
})

test("reply values carry the server's suggested identity", async function () {
  const w = await loadPage()
  const vals = w.composeValuesFromSource({ account_id: "a1", suggested_from_email: "sales@example.com", from_name: "F", from_email: "f@x.com", to: "sales@example.com", cc: "", subject: "Hi" }, "reply")
  assert.equal(vals.from_email, "sales@example.com")
  assert.equal(w.composeValuesFromSource({ account_id: "a1", subject: "x" }, "forward").from_email, "")
})
