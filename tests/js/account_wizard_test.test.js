// The account wizards test mail settings before saving them: adding tests the typed
// settings and creates the account only when asked; editing saves only when asked.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const read = function (name) { return fs.readFileSync(path.join(__dirname, "..", "..", "assets", "js", name), "utf8") }
const wizardJS = read("account-wizard.js")
const pageJS = read("settings-page.js")
// The edit wizard's part of settings-page.js; the rest of that file expects a settings page.
const editJS = pageJS.slice(pageJS.indexOf("// Edit account dialog"), pageJS.indexOf("function editToggleSmtpAuth"))
// The edit form's save handlers, from settings.js.
const settingsJS = read("settings.js")
const saveJS = settingsJS.slice(settingsJS.indexOf("function _settingsEventElement"), settingsJS.indexOf("function _calendarDiscoveryForm"))

const footerButtons = '<button data-account-test-action data-account-test-back>Back</button>' +
  '<button data-account-test-action data-account-test-anyway>Skip</button>' +
  '<button data-account-test-action data-account-test-retest hidden>Test again</button>' +
  '<button data-account-test-action data-account-test-save hidden>Save</button>' +
  '<button data-account-test-close hidden>Close</button>' +
  '<button data-account-test-continue hidden>Continue</button>'

function dom(html, replies) {
  const d = new JSDOM("<!doctype html><body>" + html + "</body>", { runScripts: "outside-only", pretendToBeVisual: true })
  const w = d.window
  const calls = []
  w.goferOnContent = function (selector, setup) { w.document.querySelectorAll(selector).forEach(setup) }
  w.fetch = function (url, options) {
    calls.push({ url: String(url), body: options && options.body })
    const reply = (replies || []).shift() || { ok: true, header: "ok", body: "<div>result</div>" }
    return Promise.resolve({
      ok: reply.ok !== false,
      text: function () { return Promise.resolve(reply.body) },
      headers: { get: function () { return reply.header } },
    })
  }
  return { w: w, calls: calls }
}
const tick = function () { return new Promise(function (r) { setTimeout(r, 5) }) }

const addDialog = '<div id="add-account-dialog">' +
  '<div data-wizard-section-steps="0,1,2,3"><button data-account-wizard-step="0" data-active="true"></button><button data-account-wizard-step="3"></button></div>' +
  '<div id="wizard-track"></div>' +
  '<form id="account-form"><input name="email_address" type="email" required value="ana@example.test"><input name="imap_host" value="imap.example.test"><input name="password" value="secret"></form>' +
  '<div id="add-account-test-results"></div>' +
  '<div id="add-contacts-content"></div>' +
  '<div id="add-wizard-footer" class="account-wizard-card-footer"><div data-wizard-footer-step="3" data-active="false" class="hidden">' + footerButtons + "</div></div>" +
  "</div>"

function setupAdd(replies, html) {
  const t = dom(html || addDialog, replies)
  t.w.eval(wizardJS)
  return t
}
const state = function (w) {
  const f = w.document.querySelector('[data-wizard-footer-step="3"]')
  const shown = function (attr) { const b = f.querySelector("[" + attr + "]"); return b && !b.hidden }
  return { anyway: shown("data-account-test-anyway"), anywayText: f.querySelector("[data-account-test-anyway]").textContent, retest: shown("data-account-test-retest"), save: shown("data-account-test-save"), close: shown("data-account-test-close"), cont: shown("data-account-test-continue") }
}

test("adding tests the typed settings first and never creates the account itself", async () => {
  const t = setupAdd([{ header: "ok", body: "<div>IMAP ok</div>" }])
  t.w.wizardGo(3, true)
  await tick()
  assert.equal(t.calls.length, 1)
  assert.equal(t.calls[0].url, "/api/accounts/test")
  assert.match(t.calls[0].body, /imap_host=imap.example.test/)
  assert.match(t.calls[0].body, /password=secret/)
  assert.equal(t.w.document.getElementById("add-account-test-results").textContent, "IMAP ok")
  assert.deepEqual(state(t.w), { anyway: false, anywayText: "Skip test and create", retest: false, save: true, close: false, cont: false })
})

test("a failing test offers create anyway and test again, and still creates nothing", async () => {
  const t = setupAdd([{ header: "failed", body: "<div>IMAP refused</div>" }, { header: "ok", body: "<div>IMAP ok</div>" }])
  t.w.wizardGo(3, true)
  await tick()
  assert.deepEqual(state(t.w), { anyway: true, anywayText: "Create anyway", retest: true, save: false, close: false, cont: false })
  t.w.eval("addAccountTest.run(true)")
  await tick()
  assert.equal(t.calls.length, 2)
  assert.ok(t.calls.every(function (c) { return c.url === "/api/accounts/test" }), "only the test endpoint is called")
  assert.equal(state(t.w).save, true)
})

test("a test request that errors shows the error and allows retrying", async () => {
  const t = setupAdd([{ ok: false, body: "all required fields must be filled in" }])
  t.w.wizardGo(3, true)
  await tick()
  assert.match(t.w.document.getElementById("add-account-test-results").textContent, /all required fields/)
  assert.equal(state(t.w).retest, true)
})

test("returning with unchanged settings keeps the result; changing a field tests again", async () => {
  const t = setupAdd([{ header: "ok", body: "<div>one</div>" }, { header: "ok", body: "<div>two</div>" }])
  t.w.wizardGo(3, true)
  await tick()
  t.w.wizardGo(3, true)
  await tick()
  assert.equal(t.calls.length, 1, "same settings: no second test")
  const input = t.w.document.querySelector('[name="imap_host"]')
  input.value = "imap2.example.test"
  input.dispatchEvent(new t.w.Event("input", { bubbles: true }))
  t.w.wizardGo(3, true)
  await tick()
  assert.equal(t.calls.length, 2)
  assert.equal(t.w.document.getElementById("add-account-test-results").textContent, "two")
})

test("an invalid form does not reach the test step", async () => {
  const t = setupAdd([], addDialog.replace('value="ana@example.test"', 'value=""'))
  t.w.document.getElementById("account-form").reportValidity = function () { return false }
  t.w.wizardGo(3, true)
  await tick()
  assert.equal(t.calls.length, 0)
})

test("creating shows progress, and a failed create explains why on the test step", async () => {
  const t = setupAdd([{ header: "ok", body: "<div>ok</div>" }])
  t.w.wizardGo(3, true)
  await tick()
  const form = t.w.document.getElementById("account-form")
  form.dispatchEvent(new t.w.CustomEvent("htmx:beforeRequest", { bubbles: true }))
  assert.match(t.w.document.getElementById("add-account-test-results").textContent, /Creating account/)
  assert.equal(t.w.document.querySelector("[data-account-test-save]").disabled, true)
  form.dispatchEvent(new t.w.CustomEvent("htmx:afterRequest", { bubbles: true, detail: { successful: false, xhr: { responseText: " This mailbox is already connected to Raven. " } } }))
  assert.match(t.w.document.getElementById("add-account-test-results").textContent, /already connected to Raven/)
  assert.equal(t.w.document.querySelector("[data-account-test-save]").disabled, false)
})

test("once the account exists the mail steps lock and the footer offers close or continue", async () => {
  const t = setupAdd([{ header: "ok", body: "<div>ok</div>" }])
  t.w.wizardGo(3, true)
  await tick()
  t.w.document.getElementById("add-contacts-content").innerHTML = "<div data-add-account-created hidden></div>"
  t.w.syncAddContactStepAfterSwap()
  assert.equal(t.w.addAccountCreated(), true)
  const s = state(t.w)
  assert.equal(s.close, true)
  assert.equal(s.cont, true)
  assert.equal(s.anyway, false)
  assert.equal(t.w.document.querySelector("[data-account-test-back]").hidden, true, "going back would create the account twice")
  assert.equal(t.w.document.querySelector('[data-account-wizard-step="0"]').disabled, true)
  t.w.wizardGo(0, true)
  assert.notEqual(t.w._wizStep, 0)
})

const editDialog = '<div id="edit-account-dialog">' +
  '<div data-wizard-section-steps="0,1,2,3"><button data-account-wizard-step="3"></button></div>' +
  '<div id="edit-wizard-track"></div>' +
  '<form id="edit-account-form"><input name="imap_host" value="imap.example.test"><input name="password" value=""></form>' +
  '<div data-edit-account-unsaved hidden></div>' +
  '<div id="edit-account-test-results" data-test-url="/api/accounts/a1/test-changes" data-test-typed="true"></div>' +
  '<div id="edit-account-save-status"></div>' +
  '<div id="edit-wizard-footer" class="account-wizard-card-footer"><div data-wizard-footer-step="3" data-active="false" class="hidden">' + footerButtons.replace("<button data-account-test-action data-account-test-save hidden>Save</button>", '<button data-account-test-action data-account-test-save hidden><span data-edit-account-save-label>Save changes</span></button>') + "</div></div>" +
  "</div>"

function setupEdit(replies) {
  const t = dom(editDialog, replies)
  t.w.eval(wizardJS)
  t.w.eval(editJS)
  t.w.eval(saveJS)
  t.w.eval("editAccountMarkSaved()")
  return t
}

test("editing tests the typed changes at 1.d and saves nothing", async () => {
  const t = setupEdit([{ header: "ok", body: "<div>ok</div>" }])
  const input = t.w.document.querySelector('[name="imap_host"]')
  input.value = "imap2.example.test"
  input.dispatchEvent(new t.w.Event("input", { bubbles: true }))
  t.w.editWizardGo(3, true)
  await tick()
  assert.equal(t.calls.length, 1)
  assert.equal(t.calls[0].url, "/api/accounts/a1/test-changes")
  assert.match(t.calls[0].body, /imap_host=imap2.example.test/)
  assert.equal(t.w.document.querySelector("[data-edit-account-save-label]").textContent, "Save changes")
  assert.equal(state(t.w).save, true)
})

test("moving on to contacts with unsaved mail changes stops at the test step", async () => {
  const t = setupEdit([{ header: "ok", body: "<div>ok</div>" }])
  const input = t.w.document.querySelector('[name="imap_host"]')
  input.value = "other.example.test"
  input.dispatchEvent(new t.w.Event("input", { bubbles: true }))
  t.w.editWizardGo(4, true)
  assert.equal(t.w._editWizStep, 3)
  assert.equal(t.w.document.querySelector("[data-edit-account-unsaved]").hidden, false)
})

test("with no changes, continuing goes straight to contacts without submitting", async () => {
  const t = setupEdit([])
  let submitted = false
  t.w.document.getElementById("edit-account-form").requestSubmit = function () { submitted = true }
  t.w.editAccountSaveOrContinue()
  assert.equal(t.w._editWizStep, 4)
  assert.equal(submitted, false)
})

test("with changes, save submits the form once; a failed save shows why and keeps the changes", async () => {
  const t = setupEdit([])
  const form = t.w.document.getElementById("edit-account-form")
  let submits = 0
  form.requestSubmit = function () { submits++ }
  const input = t.w.document.querySelector('[name="imap_host"]')
  input.value = "other.example.test"
  input.dispatchEvent(new t.w.Event("input", { bubbles: true }))
  t.w.editAccountSaveOrContinue()
  assert.equal(submits, 1)
  assert.equal(t.w._editWizStep, 0, "saving does not navigate by itself")
  t.w.document.getElementById("edit-account-save-status").innerHTML = '<div data-account-form-error><p>Failed to update account</p></div>'
  t.w.handleEditAccountSaveResult({ detail: { elt: form, successful: true, xhr: { getResponseHeader: function () { return "" } } } })
  assert.match(t.w.document.getElementById("edit-account-test-results").textContent, /Failed to update account/)
  assert.equal(t.w.editAccountChanged(), true, "unsaved changes stay unsaved")
})
