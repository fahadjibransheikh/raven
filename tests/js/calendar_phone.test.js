// Runs assets/js/calendar-phone.js in jsdom: the Agenda button and its back button.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const src = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "calendar-phone.js"), "utf8")

function load(clearHidden) {
  const dom = new JSDOM(
    '<button data-calendar-agenda-toggle></button><button data-calendar-agenda-back></button><button data-calendar-agenda-clear' + (clearHidden ? " hidden" : "") + "></button>",
    { runScripts: "outside-only" },
  )
  const w = dom.window
  w.eval(src)
  w.cleared = 0
  w.document.querySelector("[data-calendar-agenda-clear]").addEventListener("click", () => w.cleared++)
  return w
}

const attr = (w) => w.document.documentElement.hasAttribute("data-calendar-phone-agenda")

test("agenda button opens, back closes", () => {
  const w = load(true)
  w.document.querySelector("[data-calendar-agenda-toggle]").click()
  assert.equal(attr(w), true)
  w.document.querySelector("[data-calendar-agenda-back]").click()
  assert.equal(attr(w), false)
  assert.equal(w.cleared, 0)
})

test("back also clears a selected day", () => {
  const w = load(false)
  w.document.querySelector("[data-calendar-agenda-back]").click()
  assert.equal(w.cleared, 1)
})
