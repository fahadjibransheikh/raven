// Empty list wording: Add account CTA, "No results" for searches, folder-specific text otherwise.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const src = fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", "virtual-scroll.js"), "utf8")

function copy(opts) {
  const dom = new JSDOM('<!doctype html><body><aside><a hx-get="/folder/acc-trash" data-folder-role="trash"></a></aside><div id="mail-list-scroll"' + (opts.noAccounts ? " data-no-accounts" : "") + "></div></body>", { runScripts: "outside-only" })
  const w = dom.window
  w.eval(src)
  const self = Object.assign({
    container: w.document.getElementById("mail-list-scroll"),
    folderID: "inbox",
    filters: {},
    emptyFilters: function () { return w.VirtualMailList.prototype.emptyFilters.call(this) },
  }, opts.self || {})
  return w.VirtualMailList.prototype.emptyStateCopy.call(self)
}

test("no accounts offers Add account", () => {
  const c = copy({ noAccounts: true })
  assert.equal(c.ctaHref, "/settings/accounts")
  assert.equal(c.ctaLabel, "Add account")
})

test("a search with no hits says No results", () => {
  assert.equal(copy({ self: { filters: { query: "invoice" } } }).title, "No results")
  assert.equal(copy({ self: { filters: { unread: true } } }).title, "No results")
})

test("sort-only filters do not count as a search", () => {
  assert.equal(copy({ self: { filters: { sortBy: "subject", sortOrder: "asc" } } }).title, "No emails")
})

test("empty text is folder-specific", () => {
  assert.equal(copy({ self: { folderID: "inbox" } }).text, "Your inbox is empty")
  assert.equal(copy({ self: { folderID: "acc-trash" } }).text, "Trash is empty")
  assert.equal(copy({ self: { folderID: "sent" } }).text, "No sent messages yet")
  assert.equal(copy({ self: { folderID: "acc-custom" } }).text, "This folder is empty")
})
