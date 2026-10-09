// List density: the row height virtual scroll uses follows <html data-mail-density>,
// switching it re-lays-out the list, the CSS agrees with the JS, and the default
// card layout keeps subject and preview in one body zone.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const root = path.join(__dirname, "..", "..")
const virtualScroll = fs.readFileSync(path.join(root, "assets", "js", "virtual-scroll.js"), "utf8")
const uiSettings = fs.readFileSync(path.join(root, "assets", "js", "ui-settings.js"), "utf8")
const css = fs.readFileSync(path.join(root, "assets", "css", "input.css"), "utf8")

function vsWindow(density) {
  const dom = new JSDOM("<!doctype html><html" + (density ? ' data-mail-density="' + density + '"' : "") + "><body></body></html>", { runScripts: "outside-only" })
  dom.window.eval(virtualScroll)
  return dom.window
}

test("row height follows the density attribute, calm by default", () => {
  assert.equal(vsWindow().mailRowHeight("cards"), 68)
  assert.equal(vsWindow("calm").mailRowHeight("cards"), 68)
  assert.equal(vsWindow("airy").mailRowHeight("cards"), 84)
  assert.equal(vsWindow("bogus").mailRowHeight("cards"), 68)
})

test("table and contacts rows ignore the density", () => {
  const w = vsWindow("airy")
  assert.equal(w.mailRowHeight("table"), 36)
  assert.equal(w.mailRowHeight("cards", "contacts"), 64)
})

test("the CSS row heights equal the JS map", () => {
  const calm = css.match(/:root\s*{[^}]*--mail-row-height:\s*(\d+)px/)
  const airy = css.match(/:root\[data-mail-density="airy"\]\s*{[^}]*--mail-row-height:\s*(\d+)px/)
  const w = vsWindow()
  assert.equal(Number(calm[1]), w.MAIL_ROW_HEIGHT.cards.calm)
  assert.equal(Number(airy[1]), w.MAIL_ROW_HEIGHT.cards.airy)
})

function fakeList(w, itemHeight) {
  const list = {
    itemHeight,
    viewMode: "cards",
    container: { scrollTop: 68 * 5 + 17 },
    expandedThreads: new Map([["a", { measuredHeight: 238 }]]),
    visibleRows: new Map(),
    cache: new Map(),
    edgeSkeletonEl: { style: {} },
    renders: 0,
    invalidated: 0,
    prevFirst: 3,
    prevLast: 9,
    positionAtOffset(offset) { return Math.floor(offset / this.itemHeight) },
    offsetAtPosition(index) { return index * this.itemHeight },
    invalidateOffsets() { this.invalidated++ },
    render() { this.renders++ },
    updateExpandedThreadMeasuredHeight() {},
  }
  list.applyRowDensity = w.VirtualMailList.prototype.applyRowDensity
  return list
}

test("switching density updates the row height and re-renders at the same scroll anchor", () => {
  const w = vsWindow("calm")
  const list = fakeList(w, 68)
  assert.equal(list.applyRowDensity(), false, "nothing to do when the height is unchanged")
  assert.equal(list.renders, 0)

  w.document.documentElement.dataset.mailDensity = "airy"
  assert.equal(list.applyRowDensity(), true)
  assert.equal(list.itemHeight, 84)
  assert.equal(list.renders, 1)
  assert.equal(list.invalidated, 1)
  assert.equal(list.prevFirst, null, "forces the row window to be recomputed")
  assert.equal(list.expandedThreads.get("a").measuredHeight, 0, "expanded threads are re-measured")
  // row 5 stays at the top, same fraction (17/68) of the way through it
  assert.equal(list.container.scrollTop, 5 * 84 + Math.round(84 * 17 / 68))
  assert.equal(list.edgeSkeletonEl.style.height, "84px")

  w.document.documentElement.dataset.mailDensity = "calm"
  assert.equal(list.applyRowDensity(), true)
  assert.equal(list.itemHeight, 68)
})

function settingsWindow(stored) {
  const dom = new JSDOM('<!doctype html><html><body data-ui-settings=\'' + JSON.stringify(Object.assign({ timezone: "UTC" }, stored || {})) + '\'><div id="mail-list-scroll"></div></body></html>', { url: "http://localhost/", runScripts: "outside-only" })
  const w = dom.window
  w.fetch = () => Promise.resolve({ json: () => Promise.resolve({}) })
  w.eval(uiSettings)
  return w
}

test("changing the setting sets the root attribute and tells the list, with no reload", () => {
  const w = settingsWindow()
  const calls = []
  w.document.getElementById("mail-list-scroll")._virtualMailList = { applyRowDensity() { calls.push(w.document.documentElement.dataset.mailDensity) } }
  w.GoferSettings.set("mail_list_density", "airy")
  assert.equal(w.document.documentElement.dataset.mailDensity, "airy")
  assert.deepEqual(calls, ["airy"])
  w.GoferSettings.set("mail_list_density", "calm")
  assert.equal(w.document.documentElement.dataset.mailDensity, "calm")
  w.GoferSettings.set("mail_list_density", "nonsense")
  assert.equal(w.document.documentElement.dataset.mailDensity, "calm")
})

test("the stored density is applied on load", () => {
  assert.equal(settingsWindow({ mail_list_density: "airy" }).document.documentElement.dataset.mailDensity, "airy")
})

const CARD = '<a data-mail-card-layout-scope>' +
  ["railTop", "railMiddle", "railBottom", "header", "meta", "body", "footer", "status", "corner", "hidden"].map((z) => '<div data-mail-card-zone="' + z + '"></div>').join("") +
  ["avatar", "from", "date", "attachment", "unread", "subject", "preview", "labels", "thread", "starred", "account", "accountMarker", "to"].map((f) => '<span data-mail-card-field="' + f + '"></span>').join("") + "</a>"

function zones(w, scope) {
  const out = {}
  scope.querySelectorAll("[data-mail-card-zone]").forEach((z) => { out[z.dataset.mailCardZone] = [...z.querySelectorAll("[data-mail-card-field]")].map((f) => f.dataset.mailCardField).join(",") })
  return out
}

test("the default card layout combines subject and preview in the body zone", () => {
  const w = settingsWindow()
  const layout = w.getDefaultMailCardLayout()
  assert.deepEqual(Array.from(layout.body), ["subject", "preview"])
  assert.deepEqual(Array.from(layout.footer), ["labels"])
  assert.deepEqual(Array.from(layout.corner), ["thread", "starred"])
  w.document.body.insertAdjacentHTML("beforeend", CARD)
  w.applyMailCardLayoutSettings(w.document.body)
  const z = zones(w, w.document.querySelector("[data-mail-card-layout-scope]"))
  assert.equal(z.body, "subject,preview")
  assert.equal(z.footer, "labels")
  assert.equal(z.corner, "thread,starred")
  assert.equal(z.railTop, "", "text-only rows: no avatar in the rail")
  assert.equal(z.railMiddle, "accountMarker")
  assert.equal(z.hidden, "avatar,account,to")
})

test("the avatar can still be dragged back into the layout", () => {
  const layout = "railTop:avatar|header:from,date|meta:attachment,unread|railMiddle:accountMarker|body:subject,preview|status:|railBottom:|footer:labels|corner:thread,starred|hidden:account,to"
  const fields = "avatar,thread,from,accountMarker,attachment,date,unread,subject,preview,labels,starred"
  const w = settingsWindow({ mail_card_layout: layout, mail_card_fields: fields })
  w.document.body.insertAdjacentHTML("beforeend", CARD)
  w.applyMailCardLayoutSettings(w.document.body)
  assert.equal(zones(w, w.document.querySelector("[data-mail-card-layout-scope]")).railTop, "avatar")
})

test("a stored v101 default layout is read as the new default, custom layouts are kept", () => {
  const v101 = "railTop:avatar|header:from,date|meta:attachment,unread|railMiddle:|body:subject|status:|railBottom:|footer:preview,labels|corner:thread,starred|hidden:account,accountMarker,to"
  assert.deepEqual(Array.from(settingsWindow({ mail_card_layout: v101 }).getMailCardLayout().body), ["subject", "preview"])
  const custom = "railTop:avatar|header:from,date|meta:attachment,unread|railMiddle:|body:subject|status:|railBottom:|footer:preview,labels|corner:starred,thread|hidden:account,accountMarker,to"
  const kept = settingsWindow({ mail_card_layout: custom }).getMailCardLayout()
  assert.deepEqual(Array.from(kept.body), ["subject"])
  assert.deepEqual(Array.from(kept.footer), ["preview", "labels"])
})

test("the layout customiser can express and round-trip the default", () => {
  const w = settingsWindow()
  const layout = w.getDefaultMailCardLayout()
  const again = w.serializeMailCardLayout(layout)
  w.applyMailCardLayout(again)
  assert.equal(w.serializeMailCardLayout(w.getMailCardLayout()), again)
  // The avatar is hidden by default, so these layouts pass the visible fields
  // the way the layout dialog does.
  const withAvatar = "avatar,thread,from,accountMarker,attachment,date,unread,subject,preview,labels,starred"
  // preview is a text field: it may live in body or footer, never a side zone
  w.applyMailCardLayout("railTop:avatar|header:from,date|meta:attachment,unread|railMiddle:|body:subject|status:|railBottom:|footer:preview,labels|corner:starred,thread|hidden:account,accountMarker,to", withAvatar)
  assert.deepEqual(Array.from(w.getMailCardLayout().footer), ["preview", "labels"])
  w.applyMailCardLayout("railTop:avatar,preview|header:from,date|meta:attachment,unread|railMiddle:|body:subject|status:|railBottom:|footer:labels|corner:thread,starred|hidden:account,accountMarker,to", withAvatar)
  assert.deepEqual(Array.from(w.getMailCardLayout().railTop), ["avatar"], "a text field cannot land in a rail zone")
})
