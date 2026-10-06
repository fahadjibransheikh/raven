// raven.js once lacked a semicolon between its two IIFEs, so the file threw before goferConfirm /
// goferChoice were defined and every in-app confirmation was a ReferenceError.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const ravenJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "raven.js"), "utf8")

test("raven.js defines goferConfirm and goferChoice", () => {
  const w = new JSDOM("<!doctype html><body></body>", { runScripts: "outside-only" }).window
  w.eval(ravenJS)
  assert.equal(typeof w.goferConfirm, "function")
  assert.equal(typeof w.goferChoice, "function")
})
