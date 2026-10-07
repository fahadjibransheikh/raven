// Meeting-invitation card in the reading pane. The card HTML comes from GET /email/{id}/invite (rendered and escaped
// server-side from the untrusted .ics) into the [data-invite-slot] that mailview.templ puts above each message body.
// It is requested once the body iframe reports it has loaded (app.js posts "emailBodyResize"), by which time the raw
// message is on disk. Nothing here runs without a user click: the Yes/Maybe/No buttons POST to the existing
// /api/calendar/events/{id}/rsvp, "Sync calendar" POSTs /api/calendar/sync and then re-checks.
(function () {
  "use strict"

  var RESPONSE_LABEL = { accepted: "Going", tentative: "Maybe", declined: "Not going" }

  function validTZ(tz) {
    try { new Intl.DateTimeFormat("en-US", { timeZone: tz }); return true } catch (_) { return false }
  }

  // <html data-timezone> mirrors the user's timezone setting; "local" (or unset) means the browser's.
  function userTZ() {
    var tz = document.documentElement.getAttribute("data-timezone")
    if (!tz || tz === "local" || !validTZ(tz)) {
      try { tz = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC" } catch (_) { tz = "UTC" }
    }
    return tz
  }

  function fmt(ms, tz, opts) {
    return new Intl.DateTimeFormat("en-US", Object.assign({ timeZone: tz }, opts)).format(new Date(ms))
  }

  var DAY = { weekday: "short", month: "short", day: "numeric", year: "numeric" }
  var TIME = { hour: "numeric", minute: "2-digit" }

  function ymd(ms, tz) {
    var p = new Intl.DateTimeFormat("en-CA", { timeZone: tz, year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date(ms))
    return p
  }

  // Re-render the time in the user's zone; the server only knew the configured zone (or UTC when it is "local").
  function localize(card) {
    var t = card.querySelector("[data-invite-when]")
    if (!t || !t.getAttribute("data-start")) return
    var s = Date.parse(t.getAttribute("data-start"))
    var e = Date.parse(t.getAttribute("data-end"))
    if (isNaN(s)) return
    var tz = userTZ()
    var zone = (new Intl.DateTimeFormat("en-US", { timeZone: tz, timeZoneName: "short" }).formatToParts(new Date(s)).filter(function (p) { return p.type === "timeZoneName" })[0] || {}).value || ""
    var text = fmt(s, tz, DAY) + ", " + fmt(s, tz, TIME)
    if (!isNaN(e) && e > s) {
      text += " – " + (ymd(s, tz) === ymd(e, tz) ? fmt(e, tz, TIME) : fmt(e, tz, DAY) + ", " + fmt(e, tz, TIME))
    }
    t.textContent = text + (zone ? " " + zone : "")
    var open = card.querySelector("[data-invite-open]")
    if (open) open.setAttribute("href", "/calendar?date=" + ymd(s, tz))
  }

  function slotFor(emailId) {
    return document.querySelector('[data-invite-slot="' + String(emailId).replace(/[^0-9]/g, "") + '"]')
  }

  // force re-fetches a slot that was already loaded (after "Sync calendar").
  function load(emailId, force) {
    var slot = slotFor(emailId)
    if (!slot || (slot.getAttribute("data-invite-state") && !force)) return Promise.resolve(null)
    slot.setAttribute("data-invite-state", "loading")
    return window.fetch("/email/" + encodeURIComponent(emailId) + "/invite", { credentials: "same-origin", headers: { Accept: "text/html" } })
      .then(function (res) {
        if (res.status !== 200) { slot.setAttribute("data-invite-state", "none"); return null }
        return res.text().then(function (html) {
          slot.innerHTML = html
          slot.classList.remove("hidden")
          slot.setAttribute("data-invite-state", "ready")
          var card = slot.querySelector("[data-invite-card]")
          if (card) localize(card)
          return card
        })
      })
      .catch(function () { slot.removeAttribute("data-invite-state"); return null })
  }

  function showError(card, msg) {
    var el = card.querySelector("[data-invite-error]")
    if (!el) return
    el.textContent = msg || ""
    el.classList.toggle("hidden", !msg)
  }

  function post(url, body) {
    return window.fetch(url, {
      method: "POST", credentials: "same-origin",
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body: body || "",
    }).then(function (res) {
      return res.json().catch(function () { return null }).then(function (data) {
        if (!res.ok) throw new Error((data && data.message) || "Request failed (HTTP " + res.status + ")")
        return data
      })
    })
  }

  function rsvp(btn) {
    var card = btn.closest("[data-invite-card]")
    var id = card && card.getAttribute("data-event-id")
    if (!id || !/^\d+$/.test(id)) return
    var response = btn.getAttribute("data-invite-rsvp")
    var buttons = card.querySelectorAll("[data-invite-rsvp]")
    buttons.forEach(function (b) { b.disabled = true })
    showError(card, "")
    return post("/api/calendar/events/" + id + "/rsvp", "response=" + encodeURIComponent(response)).then(function () {
      buttons.forEach(function (b) { b.setAttribute("aria-pressed", b === btn ? "true" : "false") })
      var state = card.querySelector("[data-invite-response]")
      if (state) state.textContent = RESPONSE_LABEL[response] || ""
    }).catch(function (err) {
      showError(card, err.message || "Could not send your response")
    }).then(function () {
      buttons.forEach(function (b) { b.disabled = false })
    })
  }

  function sync(btn) {
    var card = btn.closest("[data-invite-card]")
    if (!card) return
    var emailId = card.getAttribute("data-email-id")
    btn.disabled = true
    showError(card, "")
    return post("/api/calendar/sync").then(function () {
      return load(emailId, true)
    }).then(function (fresh) {
      if (fresh && !fresh.getAttribute("data-event-id")) showError(fresh, "Still not in your calendar. It can take a moment to appear; try again shortly.")
    }).catch(function (err) {
      btn.disabled = false
      showError(card, err.message || "Could not sync your calendar")
    })
  }

  document.addEventListener("click", function (e) {
    var t = e.target && e.target.closest ? e.target : null
    if (!t) return
    var r = t.closest("[data-invite-rsvp]")
    if (r) { e.preventDefault(); rsvp(r); return }
    var s = t.closest("[data-invite-sync]")
    if (s) { e.preventDefault(); sync(s) }
  })

  window.addEventListener("message", function (e) {
    // The id is spliced into a request path, so only plain message ids are accepted.
    if (e.data && e.data.type === "emailBodyResize" && /^\d+$/.test(String(e.data.emailId || ""))) load(String(e.data.emailId))
  })

  window.RavenInvite = { load: load, localize: localize }
})()
