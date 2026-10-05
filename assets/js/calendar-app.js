// Calendar app. Renders the week/day grid, month, agenda, mini month, calendar list and event
// details from /api/calendar JSON. The shell comes from internal/views/calendar.templ ([data-calendar-app] is the
// #mail-list pane, [data-cal-mini]/[data-cal-list] live in the sidebar body). Everything is event-delegated on
// document and re-bound by init() after htmx swaps, so it survives the sidebar app switcher.
//
// Writes (C2): the editor, the recurring-scope prompt, delete and RSVP talk to POST/PATCH/DELETE /api/calendar/events;
// after a write the visible range is simply reload()ed. Every event element carries data-cal-event="<id>" and S.byId
// maps id -> normalized event.
(function () {
  "use strict"

  var HOUR_PX = 48
  var MIN_EVENT_MIN = 20
  var AGENDA_DAYS = 30
  var VIEWS = ["day", "week", "month", "agenda"]
  var VIEW_KEY = "raven:calendar-view"

  var S = {
    root: null,
    view: "week",
    anchor: "",
    miniMonth: "",
    tz: "UTC",
    ws: 0,
    events: [],
    byId: {},
    accounts: [],
    names: {},
    loadSeq: 0,
    syncing: null,
    autoSynced: false,
    scrollTop: null,
    openId: null,
    editor: null,
    scope: null,
  }
  var tzOverride = null
  var fmtCache = {}
  var statusTimer = null

  // ---------- time zone + civil date helpers ----------

  function browserTZ() {
    try { return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC" } catch (_) { return "UTC" }
  }

  function validTZ(tz) {
    try { new Intl.DateTimeFormat("en-US", { timeZone: tz }); return true } catch (_) { return false }
  }

  // The user's timezone setting is mirrored on <html data-timezone> by ui-settings.js ("local" means the browser's).
  function userTZ() {
    var tz = tzOverride || document.documentElement.getAttribute("data-timezone")
    if (!tz && window.GoferSettings && typeof window.GoferSettings.get === "function") tz = window.GoferSettings.get("timezone")
    if (!tz || tz === "local" || !validTZ(tz)) tz = browserTZ()
    return tz
  }

  function partsFmt(tz) {
    if (!fmtCache[tz]) {
      fmtCache[tz] = new Intl.DateTimeFormat("en-US", {
        timeZone: tz, hourCycle: "h23", year: "numeric", month: "numeric", day: "numeric",
        hour: "numeric", minute: "numeric", second: "numeric",
      })
    }
    return fmtCache[tz]
  }

  function zonedParts(ms, tz) {
    var parts = partsFmt(tz).formatToParts(new Date(ms))
    var o = {}
    for (var i = 0; i < parts.length; i++) if (parts[i].type !== "literal") o[parts[i].type] = parseInt(parts[i].value, 10)
    if (o.hour === 24) o.hour = 0
    return o
  }

  // Offset of tz from UTC at instant ms, in ms (PDT = -7h).
  function offsetMs(ms, tz) {
    var p = zonedParts(ms, tz)
    return Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second) - Math.floor(ms / 1000) * 1000
  }

  function pad2(n) { return (n < 10 ? "0" : "") + n }
  function dayStr(y, m, d) { return y + "-" + pad2(m) + "-" + pad2(d) }
  function parseDay(s) { var a = String(s).split("-"); return [parseInt(a[0], 10), parseInt(a[1], 10), parseInt(a[2], 10)] }

  function addDays(day, n) {
    var a = parseDay(day)
    var d = new Date(Date.UTC(a[0], a[1] - 1, a[2] + n))
    return dayStr(d.getUTCFullYear(), d.getUTCMonth() + 1, d.getUTCDate())
  }

  function addMonths(day, n) {
    var a = parseDay(day)
    var first = new Date(Date.UTC(a[0], a[1] - 1 + n, 1))
    var last = new Date(Date.UTC(first.getUTCFullYear(), first.getUTCMonth() + 1, 0)).getUTCDate()
    return dayStr(first.getUTCFullYear(), first.getUTCMonth() + 1, Math.min(a[2], last))
  }

  function monthStart(day) { var a = parseDay(day); return dayStr(a[0], a[1], 1) }
  function dowOf(day) { var a = parseDay(day); return new Date(Date.UTC(a[0], a[1] - 1, a[2])).getUTCDay() }
  function startOfWeek(day, ws) { return addDays(day, -((dowOf(day) - ws + 7) % 7)) }

  function dayKeyOf(ms, tz) {
    var p = zonedParts(ms, tz)
    return dayStr(p.year, p.month, p.day)
  }

  function todayStr() { return dayKeyOf(Date.now(), S.tz) }

  // Instant of wall-clock minutes-since-midnight on a civil day in tz (re-checks the offset once to land on the
  // right side of a DST change).
  function zonedToMs(day, minutes, tz) {
    var a = parseDay(day)
    var guess = Date.UTC(a[0], a[1] - 1, a[2], 0, minutes)
    var off = offsetMs(guess, tz)
    var ms = guess - off
    var off2 = offsetMs(ms, tz)
    if (off2 !== off) ms = guess - off2
    return ms
  }

  function minutesOfDay(ms, tz) {
    var p = zonedParts(ms, tz)
    return p.hour * 60 + p.minute + p.second / 60
  }

  // RFC3339 with the zone's UTC offset ("2026-10-04T00:00:00-07:00"), as the events API wants local-midnight bounds.
  function fmtRFC3339(ms, tz) {
    var p = zonedParts(ms, tz)
    var off = Math.round(offsetMs(ms, tz) / 60000)
    var sign = off < 0 ? "-" : "+"
    off = Math.abs(off)
    return dayStr(p.year, p.month, p.day) + "T" + pad2(p.hour) + ":" + pad2(p.minute) + ":" + pad2(p.second) +
      sign + pad2(Math.floor(off / 60)) + ":" + pad2(off % 60)
  }

  // ---------- ranges ----------

  // The visible days for a view. Month shows a fixed 6-week grid so the API range never exceeds 42 days.
  function rangeFor(view, anchor, ws) {
    var first, n
    if (view === "day") { first = anchor; n = 1 }
    else if (view === "week") { first = startOfWeek(anchor, ws); n = 7 }
    else if (view === "month") { first = startOfWeek(monthStart(anchor), ws); n = 42 }
    else { first = anchor; n = AGENDA_DAYS }
    var days = []
    for (var i = 0; i < n; i++) days.push(addDays(first, i))
    return { view: view, first: first, last: days[n - 1], days: days }
  }

  function eventsQuery(view, anchor, tz, ws) {
    var r = rangeFor(view, anchor, ws)
    return {
      start: fmtRFC3339(zonedToMs(r.first, 0, tz), tz),
      end: fmtRFC3339(zonedToMs(addDays(r.last, 1), 0, tz), tz),
    }
  }

  function eventsURL(view, anchor, tz, ws) {
    var q = eventsQuery(view, anchor, tz, ws)
    return "/api/calendar/events?start=" + encodeURIComponent(q.start) + "&end=" + encodeURIComponent(q.end)
  }

  // ---------- events ----------

  // Adds _startDay/_endDay (inclusive civil days in tz; all-day events use their floating dates, end_date exclusive)
  // and _sMs/_eMs for timed events.
  function normalizeEvent(ev, tz) {
    var e = {}
    for (var k in ev) e[k] = ev[k]
    if (!e.attendees) e.attendees = []
    if (e.all_day) {
      var sd = e.start_date || dayKeyOf(Date.parse(e.start), "UTC")
      var last = addDays(e.end_date || addDays(sd, 1), -1)
      e._startDay = sd
      e._endDay = last < sd ? sd : last
    } else {
      e._sMs = Date.parse(e.start)
      e._eMs = Date.parse(e.end)
      if (!(e._eMs > e._sMs)) e._eMs = e._sMs
      e._startDay = dayKeyOf(e._sMs, tz)
      e._endDay = e._eMs > e._sMs ? dayKeyOf(e._eMs - 1, tz) : e._startDay
    }
    return e
  }

  function eventsOnDay(events, day) {
    var out = []
    for (var i = 0; i < events.length; i++) if (events[i]._startDay <= day && day <= events[i]._endDay) out.push(events[i])
    out.sort(function (a, b) {
      if (!!a.all_day !== !!b.all_day) return a.all_day ? -1 : 1
      return (a._sMs || 0) - (b._sMs || 0) || String(a.title).localeCompare(String(b.title))
    })
    return out
  }

  // Overlap columns: events that transitively overlap share a cluster; each takes the first free column and the
  // cluster's column count is the divisor for width. items: [{start,end}] in any unit; returns [{col, cols}] by index.
  function layoutColumns(items) {
    var order = items.map(function (_, i) { return i }).sort(function (a, b) {
      return items[a].start - items[b].start || items[b].end - items[a].end || a - b
    })
    var out = new Array(items.length)
    var cluster = []
    var colEnds = []
    var clusterEnd = -Infinity
    function flush() {
      for (var j = 0; j < cluster.length; j++) out[cluster[j]].cols = colEnds.length
      cluster = []
      colEnds = []
      clusterEnd = -Infinity
    }
    order.forEach(function (i) {
      var it = items[i]
      if (cluster.length && it.start >= clusterEnd) flush()
      var c = 0
      while (c < colEnds.length && colEnds[c] > it.start) c++
      colEnds[c] = it.end
      out[i] = { col: c, cols: 0 }
      cluster.push(i)
      clusterEnd = Math.max(clusterEnd, it.end)
    })
    flush()
    return out
  }

  function isDeclined(ev) { return ev.self_response === "declined" }
  function isTentative(ev) { return ev.self_response === "tentative" || ev.status === "tentative" }

  function safeColor(c) { return /^#[0-9a-f]{3,8}$/i.test(c || "") ? c : "" }
  function safeURL(u) { return /^https?:\/\//i.test(u || "") ? u : "" }

  // ---------- formatting ----------

  function fmtIn(tz, opts, ms) { return new Intl.DateTimeFormat("en-US", Object.assign({ timeZone: tz }, opts)).format(ms) }
  function civil(day, opts) { var a = parseDay(day); return fmtIn("UTC", opts, Date.UTC(a[0], a[1] - 1, a[2])) }

  function fmtTime(ms, tz) { return fmtIn(tz, { hour: "numeric", minute: "2-digit" }, ms).replace(":00", "") }

  function titleFor(view, r, anchor) {
    if (view === "day") return civil(anchor, { weekday: "long", month: "long", day: "numeric", year: "numeric" })
    if (view === "month") return civil(anchor, { month: "long", year: "numeric" })
    var a = parseDay(r.first), b = parseDay(r.last)
    if (a[0] !== b[0]) return civil(r.first, { month: "short", day: "numeric", year: "numeric" }) + " – " + civil(r.last, { month: "short", day: "numeric", year: "numeric" })
    if (a[1] !== b[1]) return civil(r.first, { month: "short", day: "numeric" }) + " – " + civil(r.last, { month: "short", day: "numeric", year: "numeric" })
    return civil(r.first, { month: "short", day: "numeric" }) + " – " + b[2] + ", " + b[0]
  }

  // ---------- tiny DOM helpers (text only: nothing here ever sets innerHTML) ----------

  function h(tag, cls, text) {
    var e = document.createElement(tag)
    if (cls) e.className = cls
    if (text != null) e.textContent = text
    return e
  }

  function clear(e) { while (e.firstChild) e.removeChild(e.firstChild) }
  function $(sel) { return document.querySelector(sel) }

  function isPhone() { return document.documentElement.hasAttribute("data-phone") }

  function eventEl(ev, day, mode) {
    var el = h("button", "cal-event" + (mode === "chip" ? " is-chip" : ""))
    el.type = "button"
    el.setAttribute("data-cal-event", ev.id)
    if (isDeclined(ev)) el.classList.add("is-declined")
    if (isTentative(ev)) el.classList.add("is-tentative")
    var c = safeColor(ev.calendar_color)
    if (c) el.style.setProperty("--cal-color", c)
    // Timed events show their start time; a multi-day event only on the day it starts.
    if (!ev.all_day && ev._startDay === day) el.appendChild(h("span", "cal-event-time", fmtTime(ev._sMs, S.tz)))
    el.appendChild(h("span", "cal-event-title", ev.title || "(No title)"))
    return el
  }

  // ---------- network ----------

  function getJSON(url) {
    return window.fetch(url, { credentials: "same-origin", headers: { Accept: "application/json" } }).then(function (res) {
      if (!res.ok) throw new Error("HTTP " + res.status)
      return res.json()
    })
  }

  // The calendar endpoints are not in auth.requiresSessionCSRF, so (like the other fetch() POSTs in app.js) no
  // X-CSRF-Token is sent.
  function postForm(url, body) {
    return window.fetch(url, {
      method: "POST", credentials: "same-origin",
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body: body || "",
    }).then(function (res) {
      if (!res.ok) throw new Error("HTTP " + res.status)
      return res.json()
    })
  }

  // Write requests: errors carry the server's {error, message} so the UI can show Google's reason.
  function sendForm(method, url, params) {
    return window.fetch(url, {
      method: method, credentials: "same-origin",
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body: params ? params.toString() : undefined,
    }).then(function (res) {
      return res.json().catch(function () { return null }).then(function (data) {
        if (!res.ok) {
          var err = new Error((data && data.message) || "Request failed (HTTP " + res.status + ")")
          err.code = data && data.error
          throw err
        }
        return data
      })
    })
  }

  function toast(title, description, variant) {
    variant = variant || "success"
    if (typeof window.showGoferToast === "function") {
      window.showGoferToast({ title: title, description: description || "", variant: variant, icon: variant, position: "bottom-right", duration: variant === "error" ? 6000 : 3000, dismissible: true })
    } else {
      setStatus(description || title)
    }
  }

  function setStatus(text, sticky) {
    var el = $("[data-cal-status]")
    if (!el) return
    clearTimeout(statusTimer)
    el.textContent = text || ""
    el.classList.toggle("hidden", !text)
    if (text && !sticky) statusTimer = setTimeout(function () { setStatus("") }, 4000)
  }

  function loadCalendars() {
    var root = S.root
    return getJSON("/api/calendar/calendars").then(function (data) {
      if (S.root !== root) return
      S.accounts = (data && data.accounts) || []
      renderList()
      renderBanner()
    })
  }

  function reload() {
    var root = S.root
    if (!root) return Promise.resolve()
    var seq = ++S.loadSeq
    return getJSON(eventsURL(S.view, S.anchor, S.tz, S.ws)).then(function (data) {
      if (S.root !== root || seq !== S.loadSeq) return
      S.events = ((data && data.events) || []).map(function (e) { return normalizeEvent(e, S.tz) })
      S.byId = {}
      S.events.forEach(function (e) { S.byId[e.id] = e })
      renderBody()
    }).catch(function () {
      if (S.root === root && seq === S.loadSeq) setStatus("Could not load events")
    })
  }

  function syncAll() {
    if (S.syncing) return S.syncing
    var root = S.root
    var btn = $("[data-cal-refresh]")
    if (btn) btn.classList.add("is-spinning")
    setStatus("Syncing calendars…", true)
    S.syncing = postForm("/api/calendar/sync").then(function (data) {
      var results = (data && data.accounts) || []
      var failed = results.filter(function (r) { return r.status === "error" && r.error !== "needs_reconnect" }).length
      var running = results.some(function (r) { return r.status === "running" })
      return loadCalendars().then(function () {
        setStatus(failed ? "Sync failed for " + failed + (failed === 1 ? " account" : " accounts") : running ? "Sync already running" : "Calendars synced")
      })
    }).catch(function () {
      setStatus("Sync failed")
    }).then(function () {
      S.syncing = null
      var b = $("[data-cal-refresh]")
      if (b) b.classList.remove("is-spinning")
      return S.root === root ? reload() : null
    })
    return S.syncing
  }

  // ---------- rendering: chrome ----------

  function currentRange() { return rangeFor(S.view, S.anchor, S.ws) }

  function renderChrome() {
    var r = currentRange()
    var title = $("[data-cal-title]")
    if (title) title.textContent = titleFor(S.view, r, S.anchor)
    VIEWS.forEach(function (v) {
      var b = $('[data-cal-view="' + v + '"]')
      if (b) b.setAttribute("aria-pressed", v === S.view ? "true" : "false")
    })
    S.miniMonth = monthStart(S.anchor)
    renderMini()
  }

  function renderMini() {
    var box = $("[data-cal-mini]")
    if (!box) return
    clear(box)
    var head = h("div", "cal-mini-head")
    var prev = h("button", "", "‹")
    prev.type = "button"; prev.setAttribute("data-cal-mini-prev", ""); prev.setAttribute("aria-label", "Previous month")
    var next = h("button", "", "›")
    next.type = "button"; next.setAttribute("data-cal-mini-next", ""); next.setAttribute("aria-label", "Next month")
    head.appendChild(prev)
    head.appendChild(h("span", "", civil(S.miniMonth, { month: "long", year: "numeric" })))
    head.appendChild(next)
    box.appendChild(head)
    var grid = h("div", "cal-mini-grid")
    var first = startOfWeek(S.miniMonth, S.ws)
    for (var i = 0; i < 7; i++) grid.appendChild(h("span", "dow", civil(addDays(first, i), { weekday: "narrow" })))
    var r = currentRange()
    var today = todayStr()
    var m = parseDay(S.miniMonth)[1]
    for (var j = 0; j < 42; j++) {
      var d = addDays(first, j)
      var b = h("button", "", String(parseDay(d)[2]))
      b.type = "button"
      b.setAttribute("data-cal-mini-day", d)
      if (parseDay(d)[1] !== m) b.classList.add("other")
      if (S.view !== "month" && S.view !== "agenda" && d >= r.first && d <= r.last) b.classList.add("in-range")
      if (d === today) b.classList.add("today")
      if (d === S.anchor) b.classList.add("picked")
      grid.appendChild(b)
    }
    box.appendChild(grid)
  }

  function renderList() {
    var box = $("[data-cal-list]")
    if (!box) return
    clear(box)
    S.accounts.forEach(function (acc) {
      var sec = h("div", "cal-list-account")
      sec.appendChild(h("div", "cal-list-account-name", acc.email))
      if (!acc.calendars || !acc.calendars.length) {
        sec.appendChild(h("div", "cal-list-item", acc.needs_reconnect ? "Needs reconnect" : "No calendars yet"))
      }
      ;(acc.calendars || []).forEach(function (cal) {
        var label = h("label", "cal-list-item")
        var input = h("input")
        input.type = "checkbox"
        input.checked = !!cal.selected
        input.setAttribute("data-cal-toggle", cal.id)
        var dot = h("span", "cal-dot")
        var c = safeColor(cal.color)
        if (c) dot.style.setProperty("--cal-color", c)
        label.appendChild(input)
        label.appendChild(dot)
        label.appendChild(h("span", "min-w-0 flex-1 truncate", cal.name))
        sec.appendChild(label)
      })
      box.appendChild(sec)
    })
  }

  // One row per account that needs Google re-authorization; the button posts the same form as Settings > Accounts.
  function renderBanner() {
    var box = $("[data-cal-banner]")
    if (!box) return
    clear(box)
    var bad = S.accounts.filter(function (a) { return a.needs_reconnect })
    box.classList.toggle("hidden", !bad.length)
    bad.forEach(function (acc) {
      var row = h("div", "cal-banner")
      row.appendChild(h("span", "", "Reconnect " + acc.email + " to show its calendar"))
      var form = h("form")
      form.method = "POST"
      form.action = "/api/accounts/oauth2/authorize"
      ;[["flow_action", "reconnect"], ["provider", "gmail"], ["email_address", acc.email], ["display_name", S.names[acc.account_id] || ""]].forEach(function (kv) {
        var i = h("input")
        i.type = "hidden"; i.name = kv[0]; i.value = kv[1]
        form.appendChild(i)
      })
      var btn = h("button", "cal-btn", "Reconnect")
      btn.type = "submit"
      form.appendChild(btn)
      row.appendChild(form)
      box.appendChild(row)
    })
  }

  // ---------- rendering: views ----------

  function renderBody() {
    var body = $("[data-cal-body]")
    if (!body) return
    var prev = body.querySelector(".cal-scroll")
    if (prev) S.scrollTop = prev.scrollTop
    clear(body)
    var r = currentRange()
    if (S.view === "month") body.appendChild(renderMonth(r))
    else if (S.view === "agenda") body.appendChild(renderAgenda(r))
    else renderGrid(body, r)
    if (S.openId != null && !S.byId[S.openId]) closeDetails()
  }

  function dayHeadCell(day, today) {
    var c = h("div", "cal-head-cell" + (day === today ? " is-today" : ""))
    c.setAttribute("data-cal-goto-day", day)
    c.appendChild(h("div", "", civil(day, { weekday: "short" })))
    c.appendChild(h("strong", "", String(parseDay(day)[2])))
    return c
  }

  function renderGrid(body, r) {
    var n = r.days.length
    var today = todayStr()
    var wrap = h("div", "cal-view")
    wrap.style.setProperty("--cal-days", n)
    wrap.style.setProperty("--cal-hour", HOUR_PX + "px")
    wrap.setAttribute("data-cal-grid", S.view)

    var head = h("div", "cal-cols cal-head")
    head.appendChild(h("div"))
    r.days.forEach(function (d) { head.appendChild(dayHeadCell(d, today)) })
    wrap.appendChild(head)

    var allday = h("div", "cal-cols cal-allday")
    allday.appendChild(h("div", "cal-allday-label", "all-day"))
    r.days.forEach(function (d) {
      var cell = h("div", "cal-allday-cell")
      cell.setAttribute("data-cal-allday", d)
      eventsOnDay(S.events, d).forEach(function (ev) { if (ev.all_day) cell.appendChild(eventEl(ev, d, "chip")) })
      allday.appendChild(cell)
    })
    wrap.appendChild(allday)

    var scroll = h("div", "cal-scroll")
    var cols = h("div", "cal-cols")
    cols.style.height = 24 * HOUR_PX + "px"
    var hours = h("div", "cal-hours-col")
    for (var i = 1; i < 24; i++) {
      var lab = h("span", "cal-hour-label", fmtIn("UTC", { hour: "numeric" }, Date.UTC(2000, 0, 1, i)))
      lab.style.top = i * HOUR_PX + "px"
      hours.appendChild(lab)
    }
    cols.appendChild(hours)
    r.days.forEach(function (d) {
      var col = h("div", "cal-daycol" + (d === today ? " is-today" : ""))
      col.setAttribute("data-cal-day", d)
      var segs = []
      eventsOnDay(S.events, d).forEach(function (ev) {
        if (ev.all_day) return
        var s = ev._startDay === d ? minutesOfDay(ev._sMs, S.tz) : 0
        var e = ev._endDay === d ? minutesOfDay(ev._eMs, S.tz) : 1440
        if (ev._eMs === ev._sMs) e = s
        segs.push({ ev: ev, start: s, end: Math.max(e, s + MIN_EVENT_MIN) })
      })
      var lay = layoutColumns(segs)
      segs.forEach(function (sg, idx) {
        var el = eventEl(sg.ev, d, "block")
        el.style.top = (sg.start / 60) * HOUR_PX + "px"
        var height = Math.max(((sg.end - sg.start) / 60) * HOUR_PX - 1, 16)
        el.style.height = height + "px"
        if (height < 34) el.classList.add("is-short")
        el.style.left = (lay[idx].col / lay[idx].cols) * 100 + "%"
        el.style.width = "calc(" + 100 / lay[idx].cols + "% - 2px)"
        col.appendChild(el)
      })
      cols.appendChild(col)
    })
    scroll.appendChild(cols)
    wrap.appendChild(scroll)
    body.appendChild(wrap)
    positionNow()
    scroll.scrollTop = S.scrollTop != null ? S.scrollTop : 8 * HOUR_PX - 12 // a little above 8am so its label isn't clipped
  }

  function positionNow() {
    var old = document.querySelectorAll(".cal-now")
    for (var i = 0; i < old.length; i++) old[i].parentNode.removeChild(old[i])
    var col = $('[data-cal-day="' + todayStr() + '"]')
    if (!col || !col.classList.contains("cal-daycol")) return
    var line = h("div", "cal-now")
    line.style.top = (minutesOfDay(Date.now(), S.tz) / 60) * HOUR_PX + "px"
    col.appendChild(line)
  }

  function renderMonth(r) {
    var chips = isPhone() ? 2 : 3
    var today = todayStr()
    var m = parseDay(S.anchor)[1]
    var grid = h("div", "cal-month")
    for (var i = 0; i < 7; i++) grid.appendChild(h("div", "cal-month-dow", civil(r.days[i], { weekday: "short" })))
    r.days.forEach(function (d) {
      var cell = h("div", "cal-cell" + (parseDay(d)[1] !== m ? " is-other" : "") + (d === today ? " is-today" : ""))
      cell.setAttribute("data-cal-day", d)
      var num = h("button", "cal-cell-num", String(parseDay(d)[2]))
      num.type = "button"
      num.setAttribute("data-cal-goto-day", d)
      cell.appendChild(num)
      var evs = eventsOnDay(S.events, d)
      evs.slice(0, chips).forEach(function (ev) { cell.appendChild(eventEl(ev, d, "chip")) })
      if (evs.length > chips) {
        var more = h("button", "cal-more", "+" + (evs.length - chips) + " more")
        more.type = "button"
        more.setAttribute("data-cal-goto-day", d)
        cell.appendChild(more)
      }
      grid.appendChild(cell)
    })
    return grid
  }

  function renderAgenda(r) {
    var today = todayStr()
    var wrap = h("div", "cal-agenda")
    var any = false
    r.days.forEach(function (d) {
      var evs = eventsOnDay(S.events, d)
      if (!evs.length) return
      any = true
      var row = h("div", "cal-agenda-day")
      row.setAttribute("data-cal-day", d)
      row.appendChild(h("div", "cal-agenda-date" + (d === today ? " is-today" : ""), d === today ? "Today" : civil(d, { weekday: "short", month: "short", day: "numeric" })))
      var list = h("div")
      evs.forEach(function (ev) {
        var el = eventEl(ev, d, "chip")
        el.classList.add("cal-agenda-row")
        if (ev.location) el.appendChild(h("span", "text-muted-foreground truncate", ev.location))
        list.appendChild(el)
      })
      row.appendChild(list)
      wrap.appendChild(row)
    })
    if (!any) wrap.appendChild(h("div", "cal-empty", "No events in the next " + AGENDA_DAYS + " days"))
    return wrap
  }

  // ---------- event details ----------

  var URL_RE = /https?:\/\/[^\s<>"']+/g

  // Appends text to el with http(s) URLs as links. Everything is a text node or an <a> we build, so markup in the
  // description is shown literally and never parsed.
  function linkify(el, text) {
    text = String(text || "").replace(/<br\s*\/?>/gi, "\n")
    var last = 0
    var m
    URL_RE.lastIndex = 0
    while ((m = URL_RE.exec(text))) {
      var url = m[0].replace(/[.,;:!?)\]]+$/, "")
      if (m.index > last) el.appendChild(document.createTextNode(text.slice(last, m.index)))
      var a = h("a", "", url)
      a.href = url
      a.target = "_blank"
      a.rel = "noopener noreferrer"
      el.appendChild(a)
      last = m.index + url.length
      URL_RE.lastIndex = last
    }
    if (last < text.length) el.appendChild(document.createTextNode(text.slice(last)))
  }

  var RESPONSE = { accepted: "Accepted", declined: "Declined", tentative: "Maybe", needsAction: "Awaiting reply" }

  function whenText(ev) {
    if (ev.all_day) {
      var o = { weekday: "short", month: "short", day: "numeric", year: "numeric" }
      return ev._startDay === ev._endDay ? civil(ev._startDay, o) : civil(ev._startDay, o) + " – " + civil(ev._endDay, o)
    }
    var date = fmtIn(S.tz, { weekday: "short", month: "short", day: "numeric", year: "numeric" }, ev._sMs)
    var t = fmtTime(ev._sMs, S.tz)
    if (ev._eMs > ev._sMs) {
      var sameDay = ev._startDay === ev._endDay
      t += " – " + (sameDay ? "" : fmtIn(S.tz, { month: "short", day: "numeric" }, ev._eMs) + ", ") + fmtTime(ev._eMs, S.tz)
    }
    return date + ", " + t
  }

  function row(label, node) {
    var r = h("div", "cal-pop-row")
    r.appendChild(h("div", "cal-pop-label", label))
    r.appendChild(node)
    return r
  }

  function detailsNode(ev) {
    var box = h("div")
    var close = h("button", "cal-icon-btn cal-pop-close", "×")
    close.type = "button"
    close.setAttribute("data-cal-pop-close", "")
    close.setAttribute("aria-label", "Close")
    box.appendChild(close)
    var title = h("div", "cal-pop-title" + (isDeclined(ev) ? " is-declined" : ""), ev.title || "(No title)")
    var c = safeColor(ev.calendar_color)
    if (c) title.style.borderLeft = "4px solid " + c
    if (c) title.style.paddingLeft = "0.5rem"
    box.appendChild(title)

    var when = h("div", "", whenText(ev))
    if (!ev.all_day && ev.time_zone && ev.time_zone !== S.tz && validTZ(ev.time_zone)) {
      var own = fmtTime(ev._sMs, ev.time_zone) + (ev._eMs > ev._sMs ? " – " + fmtTime(ev._eMs, ev.time_zone) : "")
      when.appendChild(h("div", "text-muted-foreground", own + " " + ev.time_zone.replace(/_/g, " ")))
    }
    box.appendChild(row("When", when))
    if (ev.location) box.appendChild(row("Where", h("div", "", ev.location)))
    box.appendChild(row("Calendar", h("div", "", ev.calendar_name || "")))
    if (ev.organizer_email) box.appendChild(row("Organizer", h("div", "", ev.organizer_email)))
    if (canRSVP(ev)) {
      var rs = h("div", "cal-rsvp")
      ;[["accepted", "Yes"], ["tentative", "Maybe"], ["declined", "No"]].forEach(function (o) {
        var b = h("button", "cal-btn", o[1])
        b.type = "button"
        b.setAttribute("data-cal-rsvp", o[0])
        b.setAttribute("data-cal-event-id", ev.id)
        b.setAttribute("aria-pressed", ev.self_response === o[0] ? "true" : "false")
        rs.appendChild(b)
      })
      box.appendChild(row("Going?", rs))
    } else if (isTentative(ev)) box.appendChild(row("Your response", h("div", "", "Maybe")))
    else if (isDeclined(ev)) box.appendChild(row("Your response", h("div", "", "Declined")))

    var atts = (ev.attendees || []).filter(function (a) { return !a.resource })
    if (atts.length) {
      var list = h("div")
      atts.forEach(function (a) {
        var line = h("div", "cal-att")
        line.appendChild(h("span", "min-w-0 truncate" + (a.response === "declined" ? " cal-att-declined" : ""), (a.name || a.email) + (a.self ? " (you)" : "")))
        line.appendChild(h("span", "text-muted-foreground", RESPONSE[a.response] || ""))
        list.appendChild(line)
      })
      box.appendChild(row("Guests (" + atts.length + ")", list))
    }
    if (ev.description) {
      var desc = h("div", "cal-pop-desc")
      linkify(desc, ev.description)
      box.appendChild(row("Description", desc))
    }
    var actions = h("div", "cal-pop-actions")
    var join = safeURL(ev.meeting_url)
    if (join) {
      var j = h("a", "cal-pop-primary", "Join meeting")
      j.href = join; j.target = "_blank"; j.rel = "noopener noreferrer"
      actions.appendChild(j)
    }
    var open = safeURL(ev.html_link)
    if (open) {
      var o = h("a", "cal-btn cal-pop-link", "Open in Google Calendar")
      o.href = open; o.target = "_blank"; o.rel = "noopener noreferrer"
      actions.appendChild(o)
    }
    if (isWritable(ev)) {
      var edit = h("button", "cal-btn", "Edit")
      edit.type = "button"; edit.setAttribute("data-cal-edit", ev.id)
      var del = h("button", "cal-btn cal-btn-danger", "Delete")
      del.type = "button"; del.setAttribute("data-cal-delete", ev.id)
      actions.appendChild(edit)
      actions.appendChild(del)
    }
    if (actions.firstChild) box.appendChild(actions)
    return box
  }

  function showDetails(id, anchorEl) {
    var ev = S.byId[id]
    var pop = $("[data-cal-popover]")
    if (!ev || !pop) return
    S.openId = ev.id
    clear(pop)
    pop.appendChild(detailsNode(ev))
    pop.classList.remove("hidden")
    // Anchored beside the clicked event; on phones CSS makes it full screen and ignores left/top.
    var rootBox = S.root.getBoundingClientRect()
    var w = Math.min(352, rootBox.width - 16)
    var left = 8
    var top = 8
    if (anchorEl) {
      var a = anchorEl.getBoundingClientRect()
      left = a.right - rootBox.left + 8
      if (left + w > rootBox.width - 8) left = a.left - rootBox.left - w - 8
      left = Math.max(8, Math.min(left, rootBox.width - w - 8))
      top = Math.max(8, Math.min(a.top - rootBox.top, rootBox.height - pop.offsetHeight - 8))
    }
    pop.style.left = left + "px"
    pop.style.top = top + "px"
    var closeBtn = pop.querySelector("[data-cal-pop-close]")
    if (closeBtn) closeBtn.focus()
  }

  function closeDetails() {
    S.openId = null
    var pop = $("[data-cal-popover]")
    if (!pop) return false
    var was = !pop.classList.contains("hidden")
    pop.classList.add("hidden")
    clear(pop)
    return was
  }

  // ---------- writes (C2): editor, delete, RSVP ----------

  var WRITABLE = { owner: true, writer: true }
  var SLOT_MIN = 60
  var EMAIL_RE = /^[^\s@<>,;]+@[^\s@<>,;]+\.[^\s@<>,;]+$/
  var REPEATS = [["none", "Does not repeat"], ["daily", "Daily"], ["weekly", "Weekly"], ["monthly", "Monthly"], ["yearly", "Yearly"]]
  var REMINDERS = [["none", "No reminder"], ["10", "10 minutes before"], ["30", "30 minutes before"], ["60", "1 hour before"], ["1440", "1 day before"]]

  function calendarById(id) {
    for (var i = 0; i < S.accounts.length; i++) {
      var cals = S.accounts[i].calendars || []
      for (var j = 0; j < cals.length; j++) if (cals[j].id === id) return cals[j]
    }
    return null
  }

  function isWritable(ev) {
    var c = calendarById(ev.calendar_id)
    return !!(c && WRITABLE[c.access_role])
  }

  function isRecurring(ev) { return !!ev.recurring_event_id }

  // The user's own attendee entry. They can answer when they are a guest, not the organizer.
  function canRSVP(ev) {
    var me = (ev.attendees || []).filter(function (a) { return a.self })[0]
    return !!me && !me.organizer && isWritable(ev)
  }

  function writableCalendars() {
    var out = []
    S.accounts.forEach(function (acc) {
      (acc.calendars || []).forEach(function (c) { if (WRITABLE[c.access_role]) out.push({ cal: c, email: acc.email }) })
    })
    return out
  }

  // The primary calendar of the first account, else any writable calendar.
  function defaultCalendarId() {
    for (var i = 0; i < S.accounts.length; i++) {
      var cals = S.accounts[i].calendars || []
      for (var j = 0; j < cals.length; j++) if (cals[j].is_primary && WRITABLE[cals[j].access_role]) return cals[j].id
    }
    var all = writableCalendars()
    return all.length ? all[0].cal.id : null
  }

  function hhmm(ms, tz) {
    var p = zonedParts(ms, tz)
    return pad2(p.hour) + ":" + pad2(p.minute)
  }

  function hmToMin(v) {
    var m = /^(\d{1,2}):(\d{2})/.exec(v || "")
    return m ? parseInt(m[1], 10) * 60 + parseInt(m[2], 10) : NaN
  }

  function opt(value, label) {
    var o = document.createElement("option")
    o.value = value
    o.textContent = label
    return o
  }

  function input(type, cls, id) {
    var i = h("input", "cal-ed-input" + (cls ? " " + cls : ""))
    i.type = type
    if (id) i.id = "cal-ed-" + id
    return i
  }

  function edRow(label, node, forId) {
    var r = h("div", "cal-ed-row")
    var l = h("label", "cal-ed-label", label)
    if (forId) l.htmlFor = "cal-ed-" + forId
    r.appendChild(l)
    r.appendChild(node)
    return r
  }

  function setEditorError(text) {
    var ed = S.editor
    if (!ed) return
    ed.els.error.textContent = text || ""
    ed.els.error.classList.toggle("hidden", !text)
  }

  // Opens the editor. init: {ev} to edit, or {startMs,endMs} (timed) / {startDay,endDay} (all-day, endDay inclusive)
  // to create. Times are shown in the event's own zone, which is only exposed as a field when it is not the user's.
  function openEditor(init) {
    closeDetails()
    closeEditor()
    var ev = init.ev || null
    var tz = ev && !ev.all_day && ev.time_zone && validTZ(ev.time_zone) ? ev.time_zone : S.tz
    var allDay, startDay, endDay, startMs, endMs
    if (ev) {
      allDay = !!ev.all_day
      startDay = ev._startDay; endDay = ev._endDay
      startMs = ev._sMs; endMs = ev._eMs > ev._sMs ? ev._eMs : ev._sMs + SLOT_MIN * 60000
    } else {
      allDay = !!init.allDay
      startDay = init.startDay; endDay = init.endDay || init.startDay
      startMs = init.startMs; endMs = init.endMs
      if (!allDay) { startDay = dayKeyOf(startMs, tz); endDay = dayKeyOf(endMs, tz) }
    }
    var ed = S.editor = { mode: ev ? "edit" : "create", ev: ev, tz: tz, saving: false, guests: [], els: {}, dur: 0 }
    var overlay = h("div", "cal-editor-overlay")
    overlay.setAttribute("data-cal-editor", "")
    var form = h("form", "cal-editor")
    form.setAttribute("role", "dialog")
    form.setAttribute("aria-modal", "true")
    form.setAttribute("aria-label", ev ? "Edit event" : "New event")
    form.noValidate = true
    var E = ed.els

    E.title = input("text", "cal-ed-title", "title")
    E.title.placeholder = "Add title"
    E.title.maxLength = 1024
    E.title.value = ev ? ev.title || "" : ""
    form.appendChild(E.title)

    E.calendar = h("select", "cal-ed-input")
    E.calendar.id = "cal-ed-calendar"
    var calId = ev ? ev.calendar_id : defaultCalendarId()
    writableCalendars().forEach(function (c) {
      var o = opt(String(c.cal.id), c.cal.name + (S.accounts.length > 1 ? " (" + c.email + ")" : ""))
      E.calendar.appendChild(o)
    })
    if (ev && !E.calendar.querySelector('[value="' + ev.calendar_id + '"]')) E.calendar.appendChild(opt(String(ev.calendar_id), ev.calendar_name || "Calendar"))
    if (calId != null) E.calendar.value = String(calId)
    E.calendar.disabled = !!ev // moving an event between calendars is not supported
    form.appendChild(edRow("Calendar", E.calendar, "calendar"))

    E.allDay = input("checkbox", "", "allday")
    E.allDay.checked = allDay
    var ad = h("label", "cal-ed-check")
    ad.appendChild(E.allDay)
    ad.appendChild(h("span", "", "All day"))
    form.appendChild(edRow("", ad))

    E.sd = input("date", "", "sd"); E.st = input("time", "", "st")
    E.ed = input("date", "", "ed"); E.et = input("time", "", "et")
    var whenStart = h("div", "cal-ed-pair"); whenStart.appendChild(E.sd); whenStart.appendChild(E.st)
    var whenEnd = h("div", "cal-ed-pair"); whenEnd.appendChild(E.ed); whenEnd.appendChild(E.et)
    form.appendChild(edRow("Start", whenStart, "sd"))
    form.appendChild(edRow("End", whenEnd, "ed"))
    E.sd.value = startDay
    E.ed.value = endDay
    if (!allDay) { E.st.value = hhmm(startMs, tz); E.et.value = hhmm(endMs, tz) }
    else { E.st.value = "09:00"; E.et.value = "10:00" }
    ed.dur = durationOf(ed)
    function syncAllDay() {
      E.st.classList.toggle("hidden", E.allDay.checked)
      E.et.classList.toggle("hidden", E.allDay.checked)
    }
    syncAllDay()
    E.allDay.addEventListener("change", function () { syncAllDay(); ed.dur = durationOf(ed) })
    ;[E.sd, E.st].forEach(function (el) {
      el.addEventListener("change", function () { applyStartChange(ed) })
    })
    ;[E.ed, E.et].forEach(function (el) {
      el.addEventListener("change", function () { ed.dur = durationOf(ed) })
    })

    E.tz = input("text", "", "tz")
    E.tz.value = tz
    E.tz.setAttribute("autocomplete", "off")
    E.tzRow = edRow("Time zone", E.tz, "tz")
    E.tzRow.classList.toggle("hidden", tz === S.tz)
    form.appendChild(E.tzRow)

    E.repeat = h("select", "cal-ed-input")
    E.repeat.id = "cal-ed-repeat"
    if (ev && isRecurring(ev)) {
      E.repeat.appendChild(opt("", "Repeats (rule unchanged)"))
      E.repeat.disabled = true
    } else {
      REPEATS.forEach(function (r) { E.repeat.appendChild(opt(r[0], r[1])) })
    }
    form.appendChild(edRow("Repeat", E.repeat, "repeat"))

    E.location = input("text", "", "location")
    E.location.maxLength = 1024
    E.location.value = ev ? ev.location || "" : ""
    form.appendChild(edRow("Location", E.location, "location"))

    ed.guests = ev ? (ev.attendees || []).filter(function (a) { return !a.self && !a.organizer && !a.resource }).map(function (a) { return a.email }) : []
    ed.origGuests = ed.guests.slice()
    E.chips = h("div", "cal-chips")
    E.guestInput = h("input", "cal-chip-input")
    E.guestInput.type = "text"
    E.guestInput.id = "cal-ed-guests"
    E.guestInput.placeholder = "Add guests (email)"
    E.guestInput.setAttribute("autocomplete", "off")
    E.chips.appendChild(E.guestInput)
    form.appendChild(edRow("Guests", E.chips, "guests"))
    renderChips(ed)
    E.guestInput.addEventListener("keydown", function (e) {
      if (e.key === "Enter" || e.key === ",") { e.preventDefault(); e.stopPropagation(); commitGuests(ed) }
      else if (e.key === "Backspace" && !E.guestInput.value && ed.guests.length) { ed.guests.pop(); renderChips(ed) }
    })
    E.guestInput.addEventListener("blur", function () { commitGuests(ed) })
    E.chips.addEventListener("click", function (e) {
      var rm = e.target.closest && e.target.closest("[data-cal-chip-remove]")
      if (rm) { ed.guests.splice(parseInt(rm.getAttribute("data-cal-chip-remove"), 10), 1); renderChips(ed) }
    })

    E.meet = input("checkbox", "", "meet")
    var meetBox = h("label", "cal-ed-check")
    meetBox.appendChild(E.meet)
    meetBox.appendChild(h("span", "", ev && ev.meeting_url ? "Google Meet added" : "Add Google Meet video conferencing"))
    if (ev && ev.meeting_url) { E.meet.checked = true; E.meet.disabled = true }
    form.appendChild(edRow("", meetBox))

    E.description = h("textarea", "cal-ed-input cal-ed-desc")
    E.description.id = "cal-ed-description"
    E.description.rows = 3
    E.description.maxLength = 8192
    E.description.value = ev ? ev.description || "" : ""
    form.appendChild(edRow("Description", E.description, "description"))

    E.reminder = h("select", "cal-ed-input")
    E.reminder.id = "cal-ed-reminder"
    E.reminder.appendChild(opt(ev ? "" : "default", ev ? "Keep current" : "Calendar default"))
    REMINDERS.forEach(function (r) { E.reminder.appendChild(opt(r[0], r[1])) })
    form.appendChild(edRow("Reminder", E.reminder, "reminder"))

    E.error = h("div", "cal-ed-error hidden")
    E.error.setAttribute("role", "alert")
    form.appendChild(E.error)

    var actions = h("div", "cal-ed-actions")
    var cancel = h("button", "cal-btn", "Cancel")
    cancel.type = "button"
    cancel.setAttribute("data-cal-editor-cancel", "")
    cancel.addEventListener("click", closeEditor)
    E.save = h("button", "cal-pop-primary", "Save")
    E.save.type = "submit"
    E.save.setAttribute("data-cal-editor-save", "")
    actions.appendChild(cancel)
    actions.appendChild(E.save)
    form.appendChild(actions)

    form.addEventListener("submit", function (e) { e.preventDefault(); saveEditor() })
    E.title.addEventListener("keydown", function (e) {
      if (e.key === "Enter") { e.preventDefault(); e.stopPropagation(); saveEditor() }
    })
    form.addEventListener("keydown", function (e) {
      if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); saveEditor() }
    })
    overlay.appendChild(form)
    S.root.appendChild(overlay)
    ed.overlay = overlay
    E.title.focus()
  }

  function closeEditor() {
    var ed = S.editor
    if (!ed) return false
    S.editor = null
    if (ed.overlay && ed.overlay.parentNode) ed.overlay.parentNode.removeChild(ed.overlay)
    return true
  }

  // Length of the event as currently typed, in ms (days for all-day, using the inclusive end day).
  function durationOf(ed) {
    var t = readTimes(ed, true)
    return t ? t.endMs - t.startMs : 0
  }

  // Moving the start moves the end with it, keeping the length (like Google Calendar).
  function applyStartChange(ed) {
    var E = ed.els
    var t = readTimes(ed, true)
    if (!t || !(ed.dur >= 0)) return
    if (E.allDay.checked) {
      E.ed.value = addDays(E.sd.value, Math.round(ed.dur / 86400000))
    } else {
      var endMs = t.startMs + ed.dur
      E.ed.value = dayKeyOf(endMs, ed.tz)
      E.et.value = hhmm(endMs, ed.tz)
    }
  }

  // Reads the typed times. All-day: startMs/endMs are UTC midnights of the (inclusive) start and end days.
  function readTimes(ed, lenient) {
    var E = ed.els
    var tzv = E.tz ? E.tz.value.trim() : ""
    var tz = tzv && validTZ(tzv) ? tzv : ed.tz
    if (!/^\d{4}-\d{2}-\d{2}$/.test(E.sd.value) || !/^\d{4}-\d{2}-\d{2}$/.test(E.ed.value)) return null
    if (E.allDay.checked) {
      var a = parseDay(E.sd.value), b = parseDay(E.ed.value)
      return { allDay: true, tz: tz, startDay: E.sd.value, endDay: E.ed.value, startMs: Date.UTC(a[0], a[1] - 1, a[2]), endMs: Date.UTC(b[0], b[1] - 1, b[2]) }
    }
    var sm = hmToMin(E.st.value), em = hmToMin(E.et.value)
    if (isNaN(sm) || isNaN(em)) return null
    return { allDay: false, tz: tz, startDay: E.sd.value, endDay: E.ed.value, startMs: zonedToMs(E.sd.value, sm, tz), endMs: zonedToMs(E.ed.value, em, tz) }
  }

  function renderChips(ed) {
    var E = ed.els
    Array.prototype.slice.call(E.chips.querySelectorAll(".cal-chip")).forEach(function (c) { E.chips.removeChild(c) })
    ed.guests.forEach(function (email, i) {
      var chip = h("span", "cal-chip")
      chip.appendChild(h("span", "", email))
      var x = h("button", "", "×")
      x.type = "button"
      x.setAttribute("data-cal-chip-remove", i)
      x.setAttribute("aria-label", "Remove " + email)
      chip.appendChild(x)
      E.chips.insertBefore(chip, E.guestInput)
    })
  }

  // Turns whatever is typed in the guest box into chips; anything that is not an email stays in the box.
  function commitGuests(ed) {
    var E = ed.els
    var raw = E.guestInput.value.split(/[,;\s]+/).filter(Boolean)
    var rest = []
    raw.forEach(function (tok) {
      tok = tok.replace(/^<|>$/g, "")
      if (!EMAIL_RE.test(tok)) { rest.push(tok); return }
      if (!ed.guests.some(function (g) { return g.toLowerCase() === tok.toLowerCase() })) ed.guests.push(tok)
    })
    E.guestInput.value = rest.join(" ")
    if (rest.length) setEditorError("Not a valid email address: " + rest[0])
    else setEditorError("")
    renderChips(ed)
    return !rest.length
  }

  function guestKey(list) { return list.map(function (g) { return g.toLowerCase() }).sort().join(",") }

  // Resolves to "this", "series" or null (cancelled). Always shown for recurring events; non-recurring deletes get a
  // single confirm button.
  function askChoice(title, detail, choices) {
    return new Promise(function (resolve) {
      var overlay = h("div", "cal-editor-overlay cal-scope-overlay")
      overlay.setAttribute("data-cal-scope", "")
      var box = h("div", "cal-editor cal-scope")
      box.setAttribute("role", "alertdialog")
      box.setAttribute("aria-modal", "true")
      box.appendChild(h("div", "cal-pop-title", title))
      if (detail) box.appendChild(h("div", "cal-scope-detail", detail))
      var actions = h("div", "cal-ed-actions cal-scope-actions")
      function done(v) {
        S.scope = null
        if (overlay.parentNode) overlay.parentNode.removeChild(overlay)
        resolve(v)
      }
      var cancel = h("button", "cal-btn", "Cancel")
      cancel.type = "button"
      cancel.setAttribute("data-cal-scope-cancel", "")
      cancel.addEventListener("click", function () { done(null) })
      actions.appendChild(cancel)
      choices.forEach(function (c) {
        var b = h("button", c[2] || "cal-btn", c[1])
        b.type = "button"
        b.setAttribute("data-cal-scope-choice", c[0])
        b.addEventListener("click", function () { done(c[0]) })
        actions.appendChild(b)
      })
      box.appendChild(actions)
      overlay.appendChild(box)
      S.root.appendChild(overlay)
      S.scope = { cancel: function () { done(null) } }
      var first = actions.querySelector("[data-cal-scope-choice]")
      if (first) first.focus()
    })
  }

  var SCOPE_CHOICES = [["this", "This event"], ["series", "All events"]]

  function saveEditor() {
    var ed = S.editor
    if (!ed || ed.saving) return
    if (!commitGuests(ed)) return
    var E = ed.els
    var ev = ed.ev
    var tzv = E.tz.value.trim()
    if (!validTZ(tzv)) { setEditorError("Time zone must be an IANA name such as Europe/Paris"); return }
    var t = readTimes(ed)
    if (!t) { setEditorError("Enter a start and end"); return }
    if (t.allDay ? t.endMs < t.startMs : t.endMs <= t.startMs) { setEditorError("End must be after the start"); return }
    var p = new URLSearchParams()
    var timesChanged = true
    if (ev) {
      var origDay = ev._startDay, origEnd = ev._endDay
      timesChanged = t.allDay !== !!ev.all_day ||
        (t.allDay ? (t.startDay !== origDay || t.endDay !== origEnd)
          : (t.startMs !== ev._sMs || t.endMs !== (ev._eMs > ev._sMs ? ev._eMs : ev._sMs + SLOT_MIN * 60000)))
    }
    p.set("title", E.title.value.trim())
    p.set("location", E.location.value)
    p.set("description", E.description.value)
    if (timesChanged) {
      p.set("time_zone", tzv)
      if (t.allDay) {
        p.set("all_day", "1")
        p.set("start_date", t.startDay)
        p.set("end_date", addDays(t.endDay, 1)) // Google's all-day end is exclusive
      } else {
        p.set("all_day", "0")
        p.set("start", fmtRFC3339(t.startMs, tzv))
        p.set("end", fmtRFC3339(t.endMs, tzv))
      }
    }
    if (!ev || guestKey(ed.guests) !== guestKey(ed.origGuests)) p.set("attendees", ed.guests.join(","))
    if (E.meet.checked && !E.meet.disabled) p.set("add_meet", "1")
    if (!E.repeat.disabled && E.repeat.value && E.repeat.value !== "none") p.set("recurrence", E.repeat.value)
    if (E.reminder.value) p.set("reminder_minutes", E.reminder.value)

    var scopeP = ev && isRecurring(ev) ? askChoice("Edit recurring event", ev.title || "", SCOPE_CHOICES.map(function (c) { return [c[0], c[1], c[0] === "series" ? "cal-pop-primary" : "cal-btn"] })) : Promise.resolve("this")
    scopeP.then(function (scope) {
      if (!scope || S.editor !== ed) return
      ed.saving = true
      E.save.disabled = true
      E.save.textContent = "Saving…"
      setEditorError("")
      var req
      if (ev) {
        p.set("scope", scope)
        req = sendForm("PATCH", "/api/calendar/events/" + ev.id, p)
      } else {
        p.set("calendar_id", E.calendar.value)
        req = sendForm("POST", "/api/calendar/events", p)
      }
      return req.then(function () {
        if (S.editor === ed) closeEditor()
        toast(ev ? "Event saved" : "Event created")
        return reload()
      }).catch(function (err) {
        ed.saving = false
        E.save.disabled = false
        E.save.textContent = "Save"
        setEditorError(err.message)
        toast("Could not save event", err.message, "error")
      })
    })
  }

  function deleteEvent(id) {
    var ev = S.byId[id]
    if (!ev || !isWritable(ev)) return
    var choices = isRecurring(ev)
      ? [["this", "This event", "cal-btn cal-btn-danger"], ["series", "All events", "cal-btn cal-btn-danger"]]
      : [["this", "Delete", "cal-btn cal-btn-danger"]]
    return askChoice(isRecurring(ev) ? "Delete recurring event" : "Delete event?", ev.title || "(No title)", choices).then(function (scope) {
      if (!scope) return
      closeDetails()
      return sendForm("DELETE", "/api/calendar/events/" + ev.id + "?scope=" + scope).then(function () {
        toast("Event deleted")
        return reload()
      }).catch(function (err) { toast("Could not delete event", err.message, "error") })
    })
  }

  function rsvp(id, response) {
    var ev = S.byId[id]
    if (!ev || !canRSVP(ev)) return
    var p = new URLSearchParams()
    p.set("response", response)
    return sendForm("POST", "/api/calendar/events/" + ev.id + "/rsvp", p).then(function () {
      return reload()
    }).then(function () {
      var el = document.querySelector('[data-cal-event="' + id + '"]')
      if (S.byId[id] && S.openId === ev.id) showDetails(id, el)
    }).catch(function (err) { toast("Could not send response", err.message, "error") })
  }

  function editEvent(id) {
    var ev = S.byId[id]
    if (ev && isWritable(ev)) openEditor({ ev: ev })
  }

  function newTimed(day, minutes) {
    var startMs = zonedToMs(day, minutes, S.tz)
    openEditor({ startMs: startMs, endMs: zonedToMs(day, minutes + SLOT_MIN, S.tz) })
  }

  // `c`, and the New event buttons: the next half hour today, 9:00 on any other day.
  function newEventDefault() {
    if (defaultCalendarId() == null) { toast("No writable calendar", "Connect a Google account with calendar access first.", "error"); return }
    var today = todayStr()
    var day = S.anchor || today
    var min = 9 * 60
    if (day === today) min = Math.min(Math.ceil((minutesOfDay(Date.now(), S.tz) + 1) / 30) * 30, 23 * 60)
    newTimed(day, min)
  }

  // Click on an empty part of a day column: the half hour under the pointer.
  function slotClick(col, clientY) {
    var r = col.getBoundingClientRect()
    var min = Math.floor((clientY - r.top) / HOUR_PX * 2) * 30
    newTimed(col.getAttribute("data-cal-day"), Math.max(0, Math.min(min, 23 * 60 + 30)))
  }

  function allDayClick(day) {
    openEditor({ allDay: true, startDay: day, endDay: day })
  }

  // ---------- navigation ----------

  function refresh() {
    renderChrome()
    renderBody()
    return reload()
  }

  function goTo(day, view) {
    if (view && VIEWS.indexOf(view) >= 0 && view !== S.view) {
      S.view = view
      S.scrollTop = null
      try { localStorage.setItem(VIEW_KEY, view) } catch (_) {}
    }
    if (day) S.anchor = day
    closeDetails()
    return refresh()
  }

  function shift(dir) {
    var a = S.anchor
    if (S.view === "day") a = addDays(a, dir)
    else if (S.view === "week") a = addDays(a, 7 * dir)
    else if (S.view === "month") a = addMonths(a, dir)
    else a = addDays(a, AGENDA_DAYS * dir)
    return goTo(a)
  }

  function toggleCalendar(input) {
    var id = parseInt(input.getAttribute("data-cal-toggle"), 10)
    var want = input.checked
    input.disabled = true
    postForm("/api/calendar/calendars/" + id + "/selected", "selected=" + (want ? "1" : "0")).then(function () {
      S.accounts.forEach(function (a) { (a.calendars || []).forEach(function (c) { if (c.id === id) c.selected = want }) })
      // Toggling on does not sync server-side; pull the calendar's events in before redrawing.
      return want ? syncAll() : reload()
    }).catch(function () {
      input.checked = !want
      setStatus("Could not update calendar")
    }).then(function () { input.disabled = false })
  }

  // ---------- events ----------

  function typing(t) {
    if (!t || !t.tagName) return false
    var tag = t.tagName
    return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || t.isContentEditable === true ||
      !!(t.closest && t.closest('[contenteditable=""],[contenteditable="true"]'))
  }

  // Window capture + stopImmediatePropagation so mail shortcuts in app.js never see keys the calendar handles. While
  // the editor or the scope prompt is open every unmodified key is swallowed too, so `c`/`e`/`a` pressed on a button
  // inside it cannot compose, archive or reply in the mail app behind.
  function onKey(e) {
    if (!$("[data-calendar-app]")) return
    var k = e.key
    if (S.scope || S.editor) {
      if (k === "Escape") {
        if (S.scope) S.scope.cancel()
        else closeEditor()
        e.preventDefault(); e.stopImmediatePropagation()
      } else if (!typing(e.target) && !e.ctrlKey && !e.metaKey && !e.altKey && k.length === 1) {
        e.stopImmediatePropagation()
      }
      return
    }
    if (e.ctrlKey || e.metaKey || e.altKey || typing(e.target)) return
    if (k === "Escape") {
      if (closeDetails()) { e.preventDefault(); e.stopImmediatePropagation() }
      return
    }
    if (e.shiftKey || document.querySelector("dialog[open]")) return
    var handled = true
    if (k === "t") goTo(todayStr())
    else if (k === "j" || k === "ArrowRight") shift(1)
    else if (k === "k" || k === "ArrowLeft") shift(-1)
    else if (k === "d") goTo(null, "day")
    else if (k === "w") goTo(null, "week")
    else if (k === "m") goTo(null, "month")
    else if (k === "a") goTo(null, "agenda")
    else if (k === "c") newEventDefault()
    else if (k === "e") { if (S.openId != null) editEvent(S.openId) } // mail's `e` (archive) must not fire here either
    else handled = false
    if (handled) { e.preventDefault(); e.stopImmediatePropagation() }
  }

  function onClick(e) {
    var t = e.target
    if (!t || !t.closest || !$("[data-calendar-app]")) return
    var el
    if ((el = t.closest("[data-cal-event]"))) { showDetails(el.getAttribute("data-cal-event"), el); return }
    if ((el = t.closest("[data-cal-pop-close]"))) { closeDetails(); return }
    if ((el = t.closest("[data-cal-goto-day]"))) { goTo(el.getAttribute("data-cal-goto-day"), "day"); return }
    if ((el = t.closest("[data-cal-mini-day]"))) { goTo(el.getAttribute("data-cal-mini-day")); return }
    if (t.closest("[data-cal-today]")) { goTo(todayStr()); return }
    if (t.closest("[data-cal-prev]")) { shift(-1); return }
    if (t.closest("[data-cal-next]")) { shift(1); return }
    if ((el = t.closest("[data-cal-view]"))) { goTo(null, el.getAttribute("data-cal-view")); return }
    if (t.closest("[data-cal-refresh]")) { syncAll(); return }
    if (t.closest("[data-cal-mini-prev]")) { S.miniMonth = addMonths(S.miniMonth, -1); renderMini(); return }
    if (t.closest("[data-cal-mini-next]")) { S.miniMonth = addMonths(S.miniMonth, 1); renderMini(); return }
    if (t.closest("[data-cal-new]")) { newEventDefault(); return }
    if ((el = t.closest("[data-cal-edit]"))) { editEvent(el.getAttribute("data-cal-edit")); return }
    if ((el = t.closest("[data-cal-delete]"))) { deleteEvent(el.getAttribute("data-cal-delete")); return }
    if ((el = t.closest("[data-cal-rsvp]"))) { rsvp(el.getAttribute("data-cal-event-id"), el.getAttribute("data-cal-rsvp")); return }
    if (S.editor || S.scope || t.closest("[data-cal-popover]")) return
    // Empty space: a time slot (Day/Week), the all-day strip or a month cell starts a new event there.
    if (closeDetails()) return // the first click outside only dismisses the popover
    if ((el = t.closest(".cal-daycol[data-cal-day]"))) { slotClick(el, e.clientY); return }
    if ((el = t.closest("[data-cal-allday]"))) { allDayClick(el.getAttribute("data-cal-allday")); return }
    if ((el = t.closest(".cal-cell[data-cal-day]"))) { allDayClick(el.getAttribute("data-cal-day")); return }
  }

  function onChange(e) {
    var t = e.target
    if (t && t.matches && t.matches("[data-cal-toggle]")) toggleCalendar(t)
  }

  // ---------- boot ----------

  function loadView() {
    var v = null
    try { v = localStorage.getItem(VIEW_KEY) } catch (_) {}
    return VIEWS.indexOf(v) >= 0 ? v : isPhone() ? "agenda" : "week"
  }

  // Idempotent: called on load and after every htmx swap/history restore; only does work for a new calendar root.
  function init() {
    var root = $("[data-calendar-app]")
    if (!root) { S.root = null; return }
    if (root === S.root) return
    S.root = root
    S.tz = userTZ()
    S.ws = parseInt(root.getAttribute("data-week-start"), 10) || 0
    try { S.names = JSON.parse(root.getAttribute("data-account-names") || "{}") } catch (_) { S.names = {} }
    S.view = loadView()
    S.anchor = todayStr()
    S.events = []
    S.byId = {}
    S.accounts = []
    S.scrollTop = null
    S.openId = null
    renderChrome()
    renderBody()
    loadCalendars().then(function () {
      // A first-time account has nothing cached yet: pull it once so the page isn't empty.
      var fresh = S.accounts.some(function (a) { return !a.needs_reconnect && !a.last_synced_at && !(a.calendars || []).length })
      if (fresh && !S.autoSynced && S.root === root) { S.autoSynced = true; return syncAll() }
    }).catch(function () { setStatus("Could not load calendars") })
    reload()
  }

  window.addEventListener("keydown", onKey, true)
  document.addEventListener("click", onClick)
  document.addEventListener("change", onChange)
  document.addEventListener("htmx:afterSettle", init)
  document.addEventListener("htmx:historyRestore", init)
  window.addEventListener("pageshow", init)
  setInterval(function () { if ($("[data-calendar-app]")) positionNow() }, 60000)
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init)
  else init()

  window.RavenCalendar = {
    layoutColumns: layoutColumns,
    rangeFor: rangeFor,
    eventsURL: eventsURL,
    normalizeEvent: normalizeEvent,
    eventsOnDay: eventsOnDay,
    zonedToMs: zonedToMs,
    minutesOfDay: minutesOfDay,
    goTo: goTo,
    reload: reload,
    init: init,
    state: S,
    setTimeZone: function (tz) { tzOverride = tz; S.tz = userTZ() },
  }
})()
