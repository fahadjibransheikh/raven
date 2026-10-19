// A new-mail event that carries a sign-in code shows a toast whose Copy button
// writes the code to the clipboard.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")
const wait = (ms) => new Promise((r) => setTimeout(r, ms))

async function setup(clipboardResult) {
  const dom = new JSDOM("<!doctype html><body></body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const sources = []
  w.EventSource = function () { const l = {}; this.listeners = l; this.addEventListener = function (t, fn) { l[t] = fn }; this.close = function () {}; sources.push(this) }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return Promise.resolve({ ok: true, status: 200, json: function () { return Promise.resolve({}) } }) }
  const written = []
  Object.defineProperty(w.navigator, "clipboard", {
    value: { writeText: function (text) { written.push(text); return clipboardResult === "fail" ? Promise.reject(new Error("denied")) : Promise.resolve() } },
    configurable: true,
  })
  w.eval(appJS)
  if (w.document.readyState === "loading") await new Promise((r) => w.document.addEventListener("DOMContentLoaded", r))
  await wait(50)
  return { w, written, fire: (data) => sources[0].listeners["new-mail"]({ data: JSON.stringify(data) }) }
}

const newMail = { folder_id: "f-inbox", folder_role: "inbox", unread_count: 1, from_name: "Acme", subject: "Your code is 123456", code: "123456" }

test("new mail with a code shows a toast and Copy writes the code to the clipboard", async () => {
  const t = await setup()
  t.fire(newMail)
  const toast = t.w.document.getElementById("mail-code-toast")
  assert.ok(toast, "toast shown")
  assert.match(toast.textContent, /123456/)
  const copy = toast.querySelector("[data-raven-toast-secondary]")
  assert.equal(copy.textContent, "Copy")
  copy.click()
  await wait(5)
  assert.deepEqual(t.written, ["123456"])
  assert.equal(copy.textContent, "Copied")
})

test("a failed clipboard write does not claim Copied", async () => {
  const t = await setup("fail")
  t.fire(newMail)
  const copy = t.w.document.querySelector("#mail-code-toast [data-raven-toast-secondary]")
  copy.click()
  await wait(5)
  assert.notEqual(copy.textContent, "Copied")
})

test("no toast without a code, or for mail that lands in sent", async () => {
  const t = await setup()
  t.fire({ ...newMail, code: undefined })
  assert.equal(t.w.document.getElementById("mail-code-toast"), null)
  t.fire({ ...newMail, folder_role: "sent" })
  assert.equal(t.w.document.getElementById("mail-code-toast"), null)
})
