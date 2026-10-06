// Reply recipients: Reply-To wins, replying to your own message goes to its recipients,
// and display names with commas stay one recipient.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function load() {
  const dom = new JSDOM('<!doctype html><body><div data-account-id="acc" data-account-email="me@mine.com" data-account-self-emails="me@mine.com,alias@mine.com"></div></body>', { runScripts: "outside-only", pretendToBeVisual: true })
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
const base = { account_id: "acc", subject: "Hi", from_name: "Sender", from_email: "sender@x.com", to: "me@mine.com", cc: "" }

test("reply goes to Reply-To when the sender set one", async () => {
  const w = await load()
  const vals = w.composeValuesFromSource(Object.assign({}, base, { reply_to: "Support <help@x.com>" }), "reply")
  assert.equal(vals.to, "Support <help@x.com>")
})

test("reply without Reply-To goes to From", async () => {
  const w = await load()
  assert.equal(w.composeValuesFromSource(base, "reply").to, "Sender <sender@x.com>")
})

test("reply to your own sent message goes to the original To recipients", async () => {
  const w = await load()
  const own = Object.assign({}, base, { from_email: "alias@mine.com", from_name: "Me", to: "a@b.co, Carol <c@d.co>", reply_to: "ignored@x.com" })
  assert.equal(w.composeValuesFromSource(own, "reply").to, "a@b.co, Carol <c@d.co>")
  assert.equal(w.composeValuesFromSource(own, "reply-all").to, "a@b.co, Carol <c@d.co>")
})

test("display names with commas are quoted and survive splitting", async () => {
  const w = await load()
  assert.equal(w.composeAddress("Smith, Jane", "j@x.com"), '"Smith, Jane" <j@x.com>')
  const parts = Array.from(w._splitComposeRecipients('"Smith, Jane" <j@x.com>, Bob <b@x.com>; c@d.co'))
  assert.deepEqual(parts, ['"Smith, Jane" <j@x.com>', "Bob <b@x.com>", "c@d.co"])
  const vals = w.composeValuesFromSource(Object.assign({}, base, { from_name: "Smith, Jane", from_email: "j@x.com" }), "reply")
  assert.equal(vals.to, '"Smith, Jane" <j@x.com>')
  assert.equal(Array.from(w._splitComposeRecipients(vals.to)).length, 1)
})
