// "Mark all as read" must surface accounts a unified fan-out skipped.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

async function run(response) {
  const dom = new JSDOM('<!doctype html><body><button data-folder-mark-all-read="archive"></button></body>', { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.fetch = function () { return Promise.resolve({ ok: true, status: 200, json: function () { return Promise.resolve(response) } }) }
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  const toasts = []
  w.showGoferToast = function (opts) { toasts.push(opts) }
  w.document.querySelector("[data-folder-mark-all-read]").click()
  await new Promise(function (resolve) { setTimeout(resolve, 20) })
  return toasts
}

test("partial unified fan-out names the skipped accounts in an error toast", async () => {
  const toasts = await run({ updated: 2, folders: 2, skipped: ["Work Gmail"] })
  assert.equal(toasts.length, 1)
  assert.equal(toasts[0].variant, "error")
  assert.match(toasts[0].description, /Work Gmail/)
})

test("full success still shows the success toast", async () => {
  const toasts = await run({ updated: 3, folders: 3, skipped: [] })
  assert.equal(toasts.length, 1)
  assert.equal(toasts[0].variant, "success")
  assert.match(toasts[0].description, /3 messages/)
})
