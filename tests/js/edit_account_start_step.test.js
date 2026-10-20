// "Set up" next to Calendar Off opens the edit dialog at the Calendar step: the dialog opener
// used to reset the wizard to its first step after the jump.
const { test, after } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const read = (f) => fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", f), "utf8")

const windows = []
after(() => windows.forEach((w) => w.close()))

function setup() {
  const dom = new JSDOM(
    '<!doctype html><body>' +
    '<button data-edit-start-step="5" id="setup">Set up</button>' +
    '<div id="edit-account-dialog"><dialog data-tui-dialog-content open>' +
    '<button data-account-wizard-step="0" data-active="true"></button>' +
    '<button data-account-wizard-step="5" data-active="false"></button>' +
    '<div id="edit-wizard-track"></div><form id="edit-account-form"></form>' +
    '</dialog></div></body>',
    { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  windows.push(w)
  w.fetch = function () { return new Promise(function () {}) }
  w.matchMedia = function () { return { matches: false, addEventListener: function () {}, addListener: function () {} } }
  w.ravenOnContent = function () {}
  w.document.fonts = { ready: new Promise(function () {}) }
  w.eval(read("account-wizard.js"))
  w.eval(read("settings.js"))
  w.eval(read("settings-page.js"))
  return w
}

const frame = (w) => new Promise((r) => w.requestAnimationFrame(() => setTimeout(r, 0)))

test("Set up opens the edit dialog at the requested step", async () => {
  const w = setup()
  w.document.getElementById("setup").dispatchEvent(new w.MouseEvent("click", { bubbles: true }))
  w.setupEditAccountDialog()
  await frame(w)
  assert.match(w.document.getElementById("edit-wizard-track").style.transform, /translateX\(-83\.3/)
  assert.equal(w.document.querySelector('[data-account-wizard-step="5"]').dataset.active, "true")
})

test("Edit without Set up still opens at the first step", async () => {
  const w = setup()
  w.setupEditAccountDialog()
  await frame(w)
  assert.equal(w.document.getElementById("edit-wizard-track").style.transform, "translateX(0)")
})
