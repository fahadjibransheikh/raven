// Back/forward to a same-folder history entry without an email closes the open email
// without touching history.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const src = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "virtual-scroll.js"), "utf8")

function setup(selectedEmailId) {
  const dom = new JSDOM('<!doctype html><body><div id="mail-list-scroll"></div><aside></aside><div id="mail-view"><p>msg</p></div></body>', { runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.eval(src)
  const calls = { replaceUrl: 0, sync: 0, empty: 0 }
  w.setMailViewEmpty = function () { calls.empty++ }
  w._vml = {
    folderID: "inbox",
    selectedEmailId,
    filters: {},
    sidebarTag: {},
    readFiltersFromURL: function () { return {} },
    readSidebarTagFromURL: function () { return {} },
    syncFilterInputs: function () {},
    setSidebarTag: function () {},
    emptySidebarTag: function () { return {} },
    syncSelectionClasses: function () { calls.sync++ },
    replaceUrl: function () { calls.replaceUrl++ },
  }
  w.document.getElementById("mail-list-scroll")._virtualMailList = w._vml
  return { w, calls }
}

function pop(w, state) {
  const ev = new w.Event("popstate")
  ev.state = state
  w.dispatchEvent(ev)
}

test("popstate to same folder without email closes the open email, no history write", () => {
  const { w, calls } = setup("e1")
  pop(w, { folder: "inbox" })
  assert.equal(w._vml.selectedEmailId, null)
  assert.equal(calls.sync, 1)
  assert.equal(calls.empty, 1)
  assert.equal(calls.replaceUrl, 0)
})

test("popstate without email is a no-op when nothing is open", () => {
  const { w, calls } = setup(null)
  pop(w, { folder: "inbox" })
  assert.equal(calls.empty, 0)
})
