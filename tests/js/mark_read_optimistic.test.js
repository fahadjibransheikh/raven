// Opening a message must show it read at once, not after the mark-read POST returns.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const appJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "app.js"), "utf8")

const listHTML = '<div id="mail-list-scroll"><div class="mail-list-item" data-email-id="42" data-is-read="false">' +
  '<a href="/email/42" class="font-semibold"><span data-mail-card-field="unread"><span class="size-2 rounded-full bg-primary"></span></span></a>' +
  '</div></div>'

async function open(fetchResult) {
  const dom = new JSDOM("<!doctype html><body>" + listHTML + "</body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.EventSource = function () { this.addEventListener = function () {}; this.close = function () {} }
  w.matchMedia = function () { return { matches: true, addEventListener: function () {}, addListener: function () {} } }
  w.VirtualMailList = function () { return new Proxy({}, { get: function () { return function () {} } }) }
  const posts = []
  w.fetch = function (url, opts) {
    if (opts && opts.method === "POST" && /\/read\?state=read$/.test(url)) {
      posts.push(url)
      return fetchResult
    }
    return Promise.resolve({ ok: true, status: 200, json: function () { return Promise.resolve({}) }, text: function () { return Promise.resolve("") } })
  }
  w.console.warn = function () {}
  w.eval(appJS)
  if (w.document.readyState === "loading") {
    await new Promise(function (resolve) { w.document.addEventListener("DOMContentLoaded", resolve) })
  }
  const anchor = w.document.querySelector(".mail-list-item a")
  w.document.body.dispatchEvent(new w.CustomEvent("htmx:afterRequest", { detail: { pathInfo: { requestPath: "/email/42" }, elt: anchor } }))
  return { w, posts, row: w.document.querySelector(".mail-list-item"), anchor }
}

test("row shows read before the mark-read POST returns", async () => {
  const { posts, row, anchor } = await open(new Promise(function () {}))
  assert.equal(posts.length, 1)
  assert.equal(row.dataset.isRead, "true")
  assert.equal(anchor.querySelector(".bg-primary"), null)
  assert.ok(!anchor.classList.contains("font-semibold"))
})

test("row goes back to unread when the POST fails", async () => {
  const { row, anchor } = await open(Promise.resolve({ ok: false, status: 500, json: function () { return Promise.resolve({}) } }))
  await new Promise(function (resolve) { setTimeout(resolve, 20) })
  assert.equal(row.dataset.isRead, "false")
  assert.ok(anchor.querySelector(".bg-primary"))
  assert.ok(anchor.classList.contains("font-semibold"))
})
