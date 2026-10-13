// Calendar keyboard shortcuts. Every key clicks an existing control (marked data-calendar-key),
// so navigation, the new-event dialog and the rest keep their one code path.
//   t today   j/ArrowRight next   k/ArrowLeft previous   m month   w week
//   a agenda (phone)   c new event   e edit (while an event's details are open)
//   Cmd/Ctrl+Enter saves the open event dialog. Esc already closes dialogs natively.
(function () {
  var KEYS = { t: "today", j: "next", ArrowRight: "next", k: "prev", ArrowLeft: "prev", m: "month", w: "week", a: "agenda", c: "new" }

  // First visible-or-not, enabled control for the key (the New event button exists twice).
  function control(name) {
    var els = document.querySelectorAll('[data-calendar-key="' + name + '"]')
    for (var i = 0; i < els.length; i++) if (!els[i].disabled) return els[i]
    return null
  }

  function typing(t) {
    return !!(t && t.closest && t.closest("input, textarea, select, [contenteditable]:not([contenteditable='false'])"))
  }

  // The agenda button is lg:hidden; on desktop the agenda is always showing, so `a` does nothing there.
  function shown(el) {
    return !window.getComputedStyle || window.getComputedStyle(el).display !== "none"
  }

  document.addEventListener("keydown", function (e) {
    if (e.defaultPrevented || !document.getElementById("calendar-main")) return
    // The shortcut help overlay (app.js) is up: keys belong to it, not the page behind.
    if (document.getElementById("mail-shortcut-help")) return
    var el = null
    if ((e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey && e.key === "Enter") {
      el = document.querySelector("[data-calendar-create-form] [data-calendar-create-submit]")
      if (!el || el.disabled) return
    } else {
      if (e.metaKey || e.ctrlKey || e.altKey || e.shiftKey || typing(e.target)) return
      var dialogOpen = !!document.querySelector("dialog[open]")
      if (e.key === "e") el = document.querySelector("dialog[open] [data-calendar-edit-trigger]")
      else if (!dialogOpen && KEYS[e.key]) el = control(KEYS[e.key])
      if (el && KEYS[e.key] === "agenda" && !shown(el)) return
    }
    if (!el) return
    e.preventDefault()
    el.click()
  })
})()
