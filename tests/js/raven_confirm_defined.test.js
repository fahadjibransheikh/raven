// raven.js once lacked a semicolon between its two IIFEs, so the file threw before ravenConfirm /
// ravenChoice were defined and every in-app confirmation was a ReferenceError.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const ravenJS = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "raven.js"), "utf8")

test("raven.js defines ravenConfirm and ravenChoice", () => {
  const w = new JSDOM("<!doctype html><body></body>", { runScripts: "outside-only" }).window
  w.eval(ravenJS)
  assert.equal(typeof w.ravenConfirm, "function")
  assert.equal(typeof w.ravenChoice, "function")
})
