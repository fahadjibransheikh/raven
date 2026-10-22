// Raven's pages may only run scripts from its own files, so elements don't
// carry inline event handlers. Instead an element names what to do in a data
// attribute, as a JSON list: a function name and its arguments.
//
//   <button data-on-click='["wizardGo", 2]'>
//   <input data-on-change='["uploadComposeAttachments", "$files", "$this"]'>
//
// "$this" is the element, "$event" the event, "$files" the element's files
// and "$checked" whether it is checked. "$closest:<selector>" is the nearest
// ancestor matching the selector, and "$data:<name>" one of the element's
// data-* values. A function that returns false cancels the event, like
// "return f()" in an inline handler.
//
// htmx events work the same way: data-on-htmx-before-request runs on the
// element's htmx:before-request event. Only the functions listed below can be called, so
// injected markup can't use these attributes to reach anything else.
//
// <form data-confirm="Are you sure?"> is handled by raven.js, which asks in its own dialog.
;(function () {
  // Color mode "System" (data-theme-mode on <html>) follows the OS. This file loads in
  // <head>, so the dark class is set before first paint and kept in step with the OS.
  // The listener is added in every mode: switching to System later, the desktop app
  // only unpins its appearance then (window_chrome.js), which fires a change.
  var root = document.documentElement
  if (window.matchMedia) {
    var query = window.matchMedia("(prefers-color-scheme: dark)")
    if (root.getAttribute("data-theme-mode") === "system") root.classList.toggle("dark", query.matches)
    query.addEventListener("change", function (e) {
      if (root.getAttribute("data-theme-mode") !== "system") return
      root.classList.toggle("dark", e.matches)
      document.dispatchEvent(new CustomEvent("raven:system-theme-change"))
    })
  }
})()

;(function () {
  var actionNames = [
    "addAccountTest.run",
    "applyMailDiscoveryCandidate",
    "archiveThread",
    "calendarDescriptionExec",
    "calendarDescriptionSelection",
    "clearActiveMailSelection",
    "closeComposeSchedulePopover",
    "closeMobileContactView",
    "closeMobileMailView",
    "collapseComposeFullWidth",
    "collapseToDialog",
    "composeChanged",
    "composeCreateLink",
    "composeExec",
    "composeUnavailable",
    "configureCalendarCacheRequest",
    "configureCalendarSyncRequest",
    "continueEditingDraft",
    "copyCalendarMeetLink",
    "deleteMessage",
    "followUpThread",
    "deleteThread",
    "discardComposeDialog",
    "discardComposePane",
    "discardReadPaneDraft",
    "discoverMailSettings",
    "downloadMessageRaw",
    "editAccountSaveOrContinue",
    "editAccountTest.run",
    "editToggleSmtpAuth",
    "editWizardGo",
    "expandComposeFullWidth",
    "expandToPane",
    "finalizeComposeRecipientInput",
    "focusComposeRecipientField",
    "ravenClickElement",
    "ravenNavigate",
    "ravenPreventDefault",
    "ravenShowComposeRecipientField",
    "ravenSubmitHTMXForm",
    "ravenSwitchDialog",
    "handleAccountTestResult",
    "handleAccountTestStart",
    "handleCalendarDiscoveryResult",
    "handleCalendarDiscoveryStart",
    "handleCalendarGuestKeydown",
    "handleCalendarSourceSaveResult",
    "handleCalendarSourceSaveStart",
    "handleCalendarSyncAbort",
    "handleCalendarSyncResult",
    "handleCalendarSyncStart",
    "handleComposePaste",
    "handleComposeRecipientInput",
    "handleComposeRecipientKeydown",
    "handleContactSidebarSyncResult",
    "handleContactSidebarSyncStart",
    "handleEditAccountSaveResult",
    "handleEditAccountSaveStart",
    "handleMailSidebarSyncResult",
    "handleMailSidebarSyncStart",
    "handleReply",
    "markNotSpam",
    "markSpam",
    "markThreadNotSpam",
    "markThreadSpam",
    "openAddAccountDialog",
    "openICloudAccountDialog",
    "openNewCompose",
    "pasteCalendarDescription",
    "performMailAction",
    "prepareCalendarDescriptionSubmit",
    "prepareComposeSchedule",
    "printMessage",
    "promptLabelMessage",
    "removeLabelMessage",
    "retryCalendarMeetPreview",
    "retryCalendarTeams",
    "retryCalendarTeamsPreview",
    "saveActiveComposeDraft",
    "saveComposeDraft",
    "scheduleCompose",
    "selectCalendarGuest",
    "selectComposeAccount",
    "sendCompose",
    "setActiveComposeEditor",
    "showComposeSignaturePicker",
    "snoozeThread",
    "syncAddContactStepAfterSwap",
    "syncCalendarDescription",
    "syncCalendarGuestDropdownState",
    "syncSignatureEditor",
    "toggleCalendarAuth",
    "toggleCalendarDescription",
    "toggleCardDAVAuth",
    "toggleComposeAttachments",
    "toggleEmailBodySchemeById",
    "toggleRead",
    "toggleSmtpAuth",
    "toggleStar",
    "toggleThreadRead",
    "toggleThreadStar",
    "toggleUnencryptedTransportWarning",
    "triggerComposeAttachmentUpload",
    "triggerComposeInlineImageUpload",
    "unsubscribeFromMessage",
    "unsnoozeThread",
    "updateCalendarGuestDropdown",
    "updateComposeToolbar",
    "uploadComposeAttachments",
    "uploadComposeInlineImages",
    "window.tui.popover.open",
    "wizardGo",
    "wizardServiceNext",
  ]
  var allowed = Object.create(null)
  actionNames.forEach(function (name) { allowed[name] = true })

  // Resolves an allowed name like "editAccountTest.run" to its function and
  // the object it belongs to.
  function resolve(name) {
    if (!allowed[name]) return null
    var owner = window
    var parts = name.split(".")
    if (parts[0] === "window") parts.shift()
    for (var i = 0; i < parts.length - 1; i++) {
      owner = owner && owner[parts[i]]
    }
    var fn = owner && owner[parts[parts.length - 1]]
    return typeof fn === "function" ? { fn: fn, owner: owner } : null
  }

  function run(element, event, spec) {
    var list
    try { list = JSON.parse(spec) } catch (err) { return }
    if (!Array.isArray(list) || typeof list[0] !== "string") return
    var target = resolve(list[0])
    if (!target) {
      if (window.console) console.warn("Raven: unknown action", list[0])
      return
    }
    var args = list.slice(1).map(function (arg) {
      if (arg === "$this") return element
      if (arg === "$event") return event
      if (arg === "$files") return element.files
      if (arg === "$checked") return element.checked
      if (typeof arg === "string" && arg.indexOf("$closest:") === 0) return element.closest(arg.slice(9))
      if (typeof arg === "string" && arg.indexOf("$data:") === 0) return element.dataset[arg.slice(6)]
      return arg
    })
    if (target.fn.apply(target.owner, args) === false) {
      event.preventDefault()
      event.stopPropagation()
    }
  }

  // Each element gets its own listener, as an inline handler would have, so
  // actions run at the same moment and aren't stopped by other listeners.
  // Elements added later, by htmx or by scripts, are bound as they appear.
  var htmxEvents = ["before-request", "after-request", "send-abort", "config-request", "after-swap"]
  var eventTypes = ["click", "change", "input", "keydown", "keyup", "mousedown", "mouseup", "paste", "drop", "focus", "blur", "submit", "toggle"]
    .concat(htmxEvents.map(function (name) { return "htmx-" + name }))
  var attributes = eventTypes.map(function (type) { return "data-on-" + type })
  var selector = attributes.map(function (attribute) { return "[" + attribute + "]" }).join(",")

  function bindElement(element) {
    var bound = element._ravenActions || (element._ravenActions = {})
    eventTypes.forEach(function (type) {
      if (bound[type] || !element.hasAttribute("data-on-" + type)) return
      bound[type] = true
      var domEvent = type.indexOf("htmx-") === 0 ? "htmx:" + type.slice(5) : type
      element.addEventListener(domEvent, function (event) {
        var spec = element.getAttribute("data-on-" + type)
        if (spec) run(element, event, spec)
      })
    })
  }

  function bindWithin(root) {
    if (!root || root.nodeType !== 1) return
    if (root.matches(selector)) bindElement(root)
    root.querySelectorAll(selector).forEach(bindElement)
  }

  // ravenOnContent(selector, setup) runs setup(element) once for each element
  // matching selector: those on the page when it loads, and those htmx or
  // scripts add later. It replaces the setup code inline scripts ran when
  // their part of the page arrived.
  var contentSetups = []
  window.ravenOnContent = function (selector, setup) {
    var entry = { selector: selector, setup: setup, key: "_ravenSetup" + contentSetups.length }
    contentSetups.push(entry)
    if (document.readyState !== "loading") setupContent(entry)
  }
  function setupContent(entry) {
    document.querySelectorAll(entry.selector).forEach(function (element) {
      if (element[entry.key]) return
      element[entry.key] = true
      entry.setup(element)
    })
  }
  var contentScanQueued = false
  function scanContent() {
    if (contentScanQueued || !contentSetups.length) return
    contentScanQueued = true
    Promise.resolve().then(function () {
      contentScanQueued = false
      contentSetups.forEach(setupContent)
    })
  }
  document.addEventListener("DOMContentLoaded", function () { contentSetups.forEach(setupContent) })

  bindWithin(document.documentElement)
  new MutationObserver(function (mutations) {
    var added = false
    mutations.forEach(function (mutation) {
      if (mutation.type === "attributes") {
        bindElement(mutation.target)
        return
      }
      if (mutation.addedNodes.length) added = true
      mutation.addedNodes.forEach(bindWithin)
    })
    if (added && document.readyState !== "loading") scanContent()
  }).observe(document.documentElement, { childList: true, subtree: true, attributes: true, attributeFilter: attributes })

  // A poll marked data-poll-when-visible is skipped while the page is hidden
  // or the element isn't shown, which htmx can't check without running inline
  // code.
  document.addEventListener("htmx:confirm", function (event) {
    var element = event.target
    if (!element || !element.hasAttribute || !element.hasAttribute("data-poll-when-visible")) return
    if (document.visibilityState !== "visible" || element.getClientRects().length === 0) event.preventDefault()
  })

  // Small steps that used to be written inline.
  window.ravenNavigate = function (path) {
    if (typeof path === "string" && path.charAt(0) === "/" && path.charAt(1) !== "/") window.location.href = path
  }
  window.ravenClickElement = function (selector) {
    var element = document.querySelector(selector)
    if (element) element.click()
  }
  // "to" is a dialog id, or the name of a function that opens one (Raven loads
  // the add-account dialog's markup on demand).
  var dialogOpeners = { openAddAccountDialog: true, openICloudAccountDialog: true }
  window.ravenSwitchDialog = function (from, to) {
    if (window.tui && window.tui.dialog) window.tui.dialog.close(from)
    setTimeout(function () {
      if (dialogOpeners[to] && typeof window[to] === "function") window[to]()
      else if (window.tui && window.tui.dialog) window.tui.dialog.open(to)
    }, 150)
  }
  window.ravenPreventDefault = function (event) {
    event.preventDefault()
  }
  // Submits a setting as soon as it changes, on forms htmx saves.
  window.ravenSubmitHTMXForm = function (input) {
    if (input && input.form && input.form.hasAttribute("hx-post")) input.form.requestSubmit()
  }
  window.ravenShowComposeRecipientField = function (fieldID, buttonID) {
    var field = document.getElementById(fieldID)
    var button = document.getElementById(buttonID)
    if (field) field.classList.remove("hidden")
    if (button) button.classList.add("hidden")
  }
})()
