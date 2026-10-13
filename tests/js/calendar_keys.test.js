// Runs assets/js/calendar-keys.js in jsdom: keys click the controls marked data-calendar-key.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const src = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "calendar-keys.js"), "utf8")

const PAGE =
  '<div id="calendar-main"></div><input id="field">' +
  ["today", "prev", "next", "month", "week", "agenda", "new"].map((k) => '<button data-calendar-key="' + k + '"></button>').join("")

function load(extra) {
  const dom = new JSDOM(PAGE + (extra || ""), { runScripts: "outside-only" })
  const w = dom.window
  w.eval(src)
  w.clicks = []
  w.document.addEventListener("click", (e) => w.clicks.push(e.target.getAttribute("data-calendar-key") || e.target.id || e.target.tagName))
  return w
}

function press(w, key, opts, target) {
  const ev = new w.KeyboardEvent("keydown", Object.assign({ key, bubbles: true, cancelable: true }, opts))
  ;(target || w.document.body).dispatchEvent(ev)
  return ev
}

test("keys click their controls", () => {
  const w = load()
  const want = { t: "today", j: "next", ArrowRight: "next", k: "prev", ArrowLeft: "prev", m: "month", w: "week", a: "agenda", c: "new" }
  for (const [key, ctl] of Object.entries(want)) {
    w.clicks.length = 0
    const ev = press(w, key)
    assert.deepEqual(w.clicks, [ctl], key)
    assert.equal(ev.defaultPrevented, true)
  }
})

test("ignored while typing, with modifiers, or off the calendar page", () => {
  const w = load()
  press(w, "t", {}, w.document.getElementById("field"))
  press(w, "t", { metaKey: true })
  press(w, "t", { ctrlKey: true })
  press(w, "t", { altKey: true })
  press(w, "T", { shiftKey: true })
  press(w, "x")
  w.document.getElementById("calendar-main").remove()
  press(w, "t")
  assert.deepEqual(w.clicks, [])
})

test("contenteditable counts as typing", () => {
  const w = load('<div id="ce" contenteditable="true"></div>')
  press(w, "t", {}, w.document.getElementById("ce"))
  assert.deepEqual(w.clicks, [])
})

test("an open dialog blocks view keys but not Cmd+Enter save or e edit", () => {
  const w = load(
    '<dialog open><button data-calendar-edit-trigger id="edit"></button>' +
      '<form data-calendar-create-form><button type="submit" data-calendar-create-submit id="save"></button></form></dialog>',
  )
  press(w, "t")
  press(w, "c")
  assert.deepEqual(w.clicks, [])
  press(w, "e")
  assert.deepEqual(w.clicks, ["edit"])
  const ev = press(w, "Enter", { metaKey: true }, w.document.getElementById("field"))
  assert.deepEqual(w.clicks, ["edit", "save"])
  assert.equal(ev.defaultPrevented, true)
  press(w, "Enter", { ctrlKey: true })
  assert.equal(w.clicks.length, 3)
})

test("Cmd+Enter does nothing without a save button, or when it is disabled", () => {
  const w = load()
  press(w, "Enter", { metaKey: true })
  const w2 = load('<form data-calendar-create-form><button data-calendar-create-submit disabled></button></form>')
  press(w2, "Enter", { metaKey: true })
  assert.deepEqual([...w.clicks, ...w2.clicks], [])
})

test("new event skips a disabled button", () => {
  const w = load()
  const first = w.document.querySelector('[data-calendar-key="new"]')
  first.disabled = true
  const second = w.document.createElement("button")
  second.setAttribute("data-calendar-key", "new")
  second.id = "second"
  w.document.body.appendChild(second)
  press(w, "c")
  assert.deepEqual(w.clicks, ["new"])
})
