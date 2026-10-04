// Phone layout state: html[data-phone] below the lg breakpoint, and
// html[data-phone-pane="list|view"] for the mail shell. All phone styling lives
// in the @media block at the end of input.css.
(function () {
  if (!window.matchMedia) return
  var mq = window.matchMedia("(max-width: 1023.98px)")
  var root = document.documentElement
  var scheduled = false

  function syncPhone() {
    if (mq.matches) root.setAttribute("data-phone", "")
    else root.removeAttribute("data-phone")
  }

  // Mail shell only; contacts keeps its own detail mechanism.
  function syncPane() {
    scheduled = false
    var view = document.getElementById("mail-view")
    var isMail = document.getElementById("app-shell") && view && !document.getElementById("contacts-list-scroll")
    if (!isMail) {
      root.removeAttribute("data-phone-pane")
      return
    }
    root.setAttribute("data-phone-pane", view.querySelector("[data-mail-view-empty]") ? "list" : "view")
  }

  function schedulePane() {
    if (scheduled) return
    scheduled = true
    var raf = window.requestAnimationFrame || function (fn) { return setTimeout(fn, 16) }
    raf(syncPane)
  }

  syncPhone()
  if (mq.addEventListener) mq.addEventListener("change", syncPhone)
  else if (mq.addListener) mq.addListener(syncPhone)

  var shell = document.getElementById("app-shell")
  if (!shell) return
  syncPane()
  document.addEventListener("htmx:afterSettle", schedulePane)
  window.addEventListener("popstate", schedulePane)
  if (window.MutationObserver) new MutationObserver(schedulePane).observe(shell, { childList: true, subtree: true })
})()
