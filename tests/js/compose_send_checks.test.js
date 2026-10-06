// Send asks before going out with an empty subject or a mentioned-but-missing attachment.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function setup(formHTML, answer) {
  const dom = new JSDOM('<!doctype html><body><div id="compose-form" data-compose-dirty="true">' + formHTML + "</div></body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  const posts = []
  const asked = []
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function (url, o) {
    if (url === "/compose") posts.push(o.body)
    return Promise.resolve({ ok: true, json: function () { return Promise.resolve({}) } })
  }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () {} } }) }
  w.goferConfirm = function (title) { asked.push(title); return Promise.resolve(answer) }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  return { w: w, posts: posts, asked: asked }
}
const tick = function () { return new Promise(function (r) { setTimeout(r, 5) }) }
const form = function (subject, editor, extra) {
  return '<input name="to" value="a@b.co"><input name="subject" value="' + subject + '"><div data-compose-editor>' + editor + "</div>" + (extra || "")
}

test("empty subject asks first and does not send when declined", async () => {
  const t = await setup(form("", "Hello"), false)
  t.w.sendCompose(false)
  await tick()
  assert.deepEqual(t.asked, ["Send without a subject?"])
  assert.equal(t.posts.length, 0)
  assert.equal(t.w.document.getElementById("compose-form").dataset.composeSending, "false")
  t.w.close()
})

test("mentioning an attachment with none attached asks, and sends when confirmed", async () => {
  const t = await setup(form("Report", "Please see the attached file"), true)
  t.w.sendCompose(false)
  await tick()
  assert.deepEqual(t.asked, ["Forgot an attachment?"])
  assert.equal(t.posts.length, 1)
  t.w.close()
})

test("quoted text and a real attachment do not trigger the warning", async () => {
  const quoted = await setup(form("Re: Report", "Thanks<blockquote>see attached</blockquote>"), true)
  quoted.w.sendCompose(false)
  await tick()
  assert.equal(quoted.asked.length, 0)
  assert.equal(quoted.posts.length, 1)
  quoted.w.close()

  const attached = await setup(form("Report", "attached", '<div data-compose-attachment></div>'), true)
  attached.w.sendCompose(false)
  await tick()
  assert.equal(attached.asked.length, 0)
  attached.w.close()
})
