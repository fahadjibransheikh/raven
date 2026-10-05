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
    else {
      root.removeAttribute("data-phone")
      setNav(false)
    }
  }

  // Folders/accounts drawer state lives on html[data-phone-nav]; toggles mirror it in aria-expanded.
  function setNav(open) {
    if (open) root.setAttribute("data-phone-nav", "open")
    else root.removeAttribute("data-phone-nav")
    var toggles = document.querySelectorAll("[data-phone-nav-toggle]")
    for (var i = 0; i < toggles.length; i++) toggles[i].setAttribute("aria-expanded", open ? "true" : "false")
  }

  // Mail shell only; contacts and calendar keep their own detail mechanisms.
  function syncPane() {
    scheduled = false
    var view = document.getElementById("mail-view")
    var isMail = document.getElementById("app-shell") && view && !document.getElementById("contacts-list-scroll") && !document.querySelector("[data-calendar-app]")
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
  var backdrop = document.createElement("div")
  backdrop.id = "phone-nav-backdrop"
  backdrop.addEventListener("click", function () { setNav(false) })
  document.body.appendChild(backdrop)

  document.addEventListener("click", function (e) {
    var t = e.target
    if (!t || !t.closest) return
    if (t.closest("[data-phone-nav-toggle]")) {
      setNav(root.getAttribute("data-phone-nav") !== "open")
    } else if (root.hasAttribute("data-phone-nav") && t.closest("[data-phone-drawer] a")) {
      setNav(false)
    }
  }, true)
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && root.hasAttribute("data-phone-nav")) setNav(false)
  })
  document.addEventListener("htmx:afterSettle", function (e) {
    var elt = e.detail && e.detail.elt
    if (elt && elt.closest && elt.closest("[data-phone-drawer]")) setNav(false)
  })

  syncPane()
  document.addEventListener("htmx:afterSettle", schedulePane)
  window.addEventListener("popstate", schedulePane)
  if (window.MutationObserver) new MutationObserver(schedulePane).observe(shell, { childList: true, subtree: true })
})()
