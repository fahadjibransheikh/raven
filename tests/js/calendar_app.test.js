// Runs assets/js/calendar-app.js in jsdom against the real templ shell (scripts/render-calendar) with a mocked
// /api/calendar backend. Run with `npm test` (or `task test`).
const { test, after } = require("node:test")
const assert = require("node:assert/strict")
const { execFileSync } = require("node:child_process")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const root = path.join(__dirname, "..", "..")
const shell = execFileSync("go", ["run", "./scripts/render-calendar"], { cwd: root, encoding: "utf8" })
const src = fs.readFileSync(path.join(root, "assets", "js", "calendar-app.js"), "utf8")

const LA = "America/Los_Angeles"
// calendar-app.js keeps a 60s interval (now-line); close every window so node can exit.
const windows = []
after(() => windows.forEach((w) => w.close()))
const flush = async () => { for (let i = 0; i < 8; i++) await new Promise((r) => setTimeout(r, 0)) }

function ev(over) {
  return Object.assign({
    id: 1, calendar_id: 10, account_id: "acc1", title: "Event", start: "2026-10-07T16:00:00Z", end: "2026-10-07T17:00:00Z",
    all_day: false, status: "confirmed", attendees: [], calendar_name: "Work", calendar_color: "#3366cc",
  }, over)
}

const defaultCalendars = {
  accounts: [{
    account_id: "acc1", email: "ann@example.com", needs_reconnect: false, last_synced_at: "2026-10-01T00:00:00Z",
    calendars: [{ id: 10, account_id: "acc1", name: "Work", color: "#3366cc", selected: true }],
  }],
}

// opts: events, calendars, tz, body (default: the real shell), nowait
async function load(opts) {
  opts = opts || {}
  const dom = new JSDOM("<!doctype html><html data-timezone=\"" + (opts.tz || LA) + "\"><body>" + (opts.body === undefined ? shell : opts.body) + "</body></html>",
    { url: "http://localhost/calendar", runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  windows.push(w)
  const calls = []
  w.fetch = (url, init) => {
    calls.push({ url, method: (init && init.method) || "GET", body: (init && init.body) || "" })
    let data = {}
    if (url.indexOf("/api/calendar/events") === 0) data = { events: opts.events || [] }
    else if (url === "/api/calendar/calendars") data = opts.calendars || defaultCalendars
    else if (url === "/api/calendar/sync") data = { accounts: [{ account_id: "acc1", status: "ok" }] }
    else if (/\/selected$/.test(url)) data = { id: 10, selected: true }
    return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(data) })
  }
  w.eval(src)
  await flush()
  return { w, d: w.document, calls, cal: w.RavenCalendar }
}

const key = (w, target, k, extra) => {
  const e = new w.KeyboardEvent("keydown", Object.assign({ key: k, bubbles: true, cancelable: true }, extra))
  target.dispatchEvent(e)
  return e
}
const eventsCalls = (calls) => calls.filter((c) => c.url.indexOf("/api/calendar/events") === 0)
const q = (url, name) => new URL(url, "http://x").searchParams.get(name)

test("layoutColumns", async () => {
  const { cal } = await load({ body: "" })
  const lay = (items) => JSON.parse(JSON.stringify(cal.layoutColumns(items)))
  assert.deepEqual(lay([{ start: 0, end: 60 }, { start: 30, end: 90 }]), [{ col: 0, cols: 2 }, { col: 1, cols: 2 }])
  assert.deepEqual(lay([{ start: 0, end: 60 }, { start: 60, end: 120 }]), [{ col: 0, cols: 1 }, { col: 0, cols: 1 }])
  // A overlaps B, B overlaps C, A and C do not: one cluster of 2 columns, C reuses A's column.
  assert.deepEqual(lay([{ start: 0, end: 60 }, { start: 30, end: 120 }, { start: 70, end: 100 }]),
    [{ col: 0, cols: 2 }, { col: 1, cols: 2 }, { col: 0, cols: 2 }])
  // Three mutually overlapping events, input order scrambled: result is indexed by input position.
  assert.deepEqual(lay([{ start: 20, end: 80 }, { start: 0, end: 80 }, { start: 10, end: 80 }]),
    [{ col: 2, cols: 3 }, { col: 0, cols: 3 }, { col: 1, cols: 3 }])
  assert.deepEqual(lay([]), [])
})

test("all-day events use start_date and exclusive end_date", async () => {
  const { cal } = await load({ body: "" })
  const one = cal.normalizeEvent(ev({ id: 1, all_day: true, start_date: "2026-10-05", end_date: "2026-10-06", start: "2026-10-05T00:00:00Z", end: "2026-10-06T00:00:00Z" }), LA)
  const three = cal.normalizeEvent(ev({ id: 2, all_day: true, start_date: "2026-10-05", end_date: "2026-10-08", start: "2026-10-05T00:00:00Z", end: "2026-10-08T00:00:00Z" }), LA)
  const days = []
  for (let n = 3; n <= 9; n++) days.push("2026-10-0" + n)
  const on = (e) => days.filter((d) => cal.eventsOnDay([e], d).length)
  assert.deepEqual(on(one), ["2026-10-05"])
  assert.deepEqual(on(three), ["2026-10-05", "2026-10-06", "2026-10-07"])
})

test("all-day events render once per day in the all-day row (not shifted by the time zone)", async () => {
  const events = [ev({ id: 2, title: "Trip", all_day: true, start_date: "2026-10-05", end_date: "2026-10-08", start: "2026-10-05T00:00:00Z", end: "2026-10-08T00:00:00Z" })]
  const { d, cal } = await load({ events })
  await cal.goTo("2026-10-07", "week")
  await flush()
  const cells = [...d.querySelectorAll("[data-cal-allday]")].filter((c) => c.querySelector("[data-cal-event]")).map((c) => c.getAttribute("data-cal-allday"))
  assert.deepEqual(cells, ["2026-10-05", "2026-10-06", "2026-10-07"])
})

test("timed events are placed in the user's time zone (16:00Z is 09:00 in Los Angeles)", async () => {
  const events = [ev({ title: "Standup" })]
  const la = await load({ events, tz: LA })
  await la.cal.goTo("2026-10-07", "week")
  await flush()
  const el = la.d.querySelector('[data-cal-day="2026-10-07"] [data-cal-event]')
  assert.ok(el, "event rendered on Oct 7")
  assert.equal(el.style.top, "432px") // 9 * 48
  assert.match(el.textContent, /9 AM/)

  const utc = await load({ events, tz: "UTC" })
  await utc.cal.goTo("2026-10-07", "week")
  await flush()
  assert.equal(utc.d.querySelector('[data-cal-day="2026-10-07"] [data-cal-event]').style.top, "768px") // 16 * 48
})

test("overlapping timed events render side by side", async () => {
  const events = [ev({ id: 1, title: "A" }), ev({ id: 2, title: "B", start: "2026-10-07T16:30:00Z", end: "2026-10-07T17:30:00Z" })]
  const { d, cal } = await load({ events })
  await cal.goTo("2026-10-07", "day")
  await flush()
  const els = [...d.querySelectorAll('[data-cal-day="2026-10-07"] [data-cal-event]')]
  assert.deepEqual(els.map((e) => e.style.left), ["0%", "50%"])
  assert.ok(els.every((e) => e.style.width === "calc(50% - 2px)"))
})

test("range bounds sent to the API carry the user's UTC offset", async () => {
  const { calls, cal } = await load({})
  await cal.goTo("2026-10-07", "week")
  await flush()
  let last = eventsCalls(calls).pop()
  assert.equal(q(last.url, "start"), "2026-10-04T00:00:00-07:00")
  assert.equal(q(last.url, "end"), "2026-10-11T00:00:00-07:00")

  // Month = fixed 6-week grid from the Sunday on/before the 1st; DST ends Nov 1 so the end bound is -08:00.
  await cal.goTo("2026-10-07", "month")
  await flush()
  last = eventsCalls(calls).pop()
  assert.equal(q(last.url, "start"), "2026-09-27T00:00:00-07:00")
  assert.equal(q(last.url, "end"), "2026-11-08T00:00:00-08:00")

  await cal.goTo("2026-10-07", "day")
  await flush()
  last = eventsCalls(calls).pop()
  assert.equal(q(last.url, "start"), "2026-10-07T00:00:00-07:00")
  assert.equal(q(last.url, "end"), "2026-10-08T00:00:00-07:00")
})

test("declined events are struck/faded and tentative ones hatched via classes", async () => {
  const events = [
    ev({ id: 1, title: "No", self_response: "declined" }),
    ev({ id: 2, title: "Maybe", self_response: "tentative", start: "2026-10-07T18:00:00Z", end: "2026-10-07T19:00:00Z" }),
    ev({ id: 3, title: "Hold", status: "tentative", start: "2026-10-07T20:00:00Z", end: "2026-10-07T21:00:00Z" }),
    ev({ id: 4, title: "Yes", self_response: "accepted", start: "2026-10-07T22:00:00Z", end: "2026-10-07T23:00:00Z" }),
  ]
  const { d, cal } = await load({ events })
  await cal.goTo("2026-10-07", "day")
  await flush()
  const by = (id) => d.querySelector('[data-cal-event="' + id + '"]')
  assert.ok(by(1).classList.contains("is-declined") && !by(1).classList.contains("is-tentative"))
  assert.ok(by(2).classList.contains("is-tentative") && !by(2).classList.contains("is-declined"))
  assert.ok(by(3).classList.contains("is-tentative"))
  assert.ok(!by(4).classList.contains("is-declined") && !by(4).classList.contains("is-tentative"))
  assert.equal(by(1).style.getPropertyValue("--cal-color"), "#3366cc")
})

test("event details show the description as text; markup is not executed", async () => {
  const events = [ev({
    description: "<script>window.__pwned = 1</script><img src=x onerror=\"window.__pwned = 2\">\nAgenda: https://example.com/doc?a=1&b=2. javascript:alert(1)",
    location: "Room 4", organizer_email: "boss@example.com", meeting_url: "https://meet.example.com/abc", html_link: "https://calendar.google.com/event?eid=1",
    attendees: [{ email: "me@example.com", response: "accepted", self: true }, { email: "x@example.com", name: "Xi", response: "declined" }],
  })]
  const { w, d, cal } = await load({ events })
  await cal.goTo("2026-10-07", "week")
  await flush()
  d.querySelector("[data-cal-event]").click()
  const pop = d.querySelector("[data-cal-popover]")
  assert.ok(!pop.classList.contains("hidden"))
  assert.equal(pop.querySelector("script"), null)
  assert.equal(pop.querySelector("img"), null)
  assert.equal(w.__pwned, undefined)
  const desc = pop.querySelector(".cal-pop-desc")
  assert.ok(desc.textContent.includes("<script>window.__pwned = 1</script>"))
  const links = [...desc.querySelectorAll("a")]
  assert.deepEqual(links.map((a) => a.getAttribute("href")), ["https://example.com/doc?a=1&b=2"])
  assert.equal(links[0].rel, "noopener noreferrer")
  assert.ok(desc.textContent.includes("javascript:alert(1)")) // plain text, not a link
  const hrefs = [...pop.querySelectorAll("a")].map((a) => a.textContent + "=" + a.getAttribute("href"))
  assert.ok(hrefs.includes("Join meeting=https://meet.example.com/abc"))
  assert.ok(hrefs.includes("Open in Google Calendar=https://calendar.google.com/event?eid=1"))
  assert.match(pop.textContent, /Room 4/)
  assert.match(pop.textContent, /boss@example.com/)
  assert.match(pop.textContent, /Xi/)
  assert.match(pop.textContent, /Declined/)
  assert.match(pop.textContent, /Wed, Oct 7, 2026, 9 AM – 10 AM/)
  // Esc closes
  key(w, d.body, "Escape")
  assert.ok(pop.classList.contains("hidden"))
})

test("details mention the event's own time zone when it differs", async () => {
  const events = [ev({ time_zone: "America/New_York" })]
  const { d, cal } = await load({ events })
  await cal.goTo("2026-10-07", "week")
  await flush()
  d.querySelector("[data-cal-event]").click()
  assert.match(d.querySelector("[data-cal-popover]").textContent, /12 PM – 1 PM America\/New York/)
})

test("javascript: meeting and link URLs are dropped", async () => {
  const events = [ev({ meeting_url: "javascript:alert(1)", html_link: "data:text/html,x" })]
  const { d, cal } = await load({ events })
  await cal.goTo("2026-10-07", "week")
  await flush()
  d.querySelector("[data-cal-event]").click()
  assert.equal(d.querySelector("[data-cal-popover] a"), null)
})

test("month view caps chips and +N more opens that day", async () => {
  const events = []
  for (let i = 0; i < 5; i++) events.push(ev({ id: 100 + i, title: "E" + i, start: "2026-10-07T" + (15 + i) + ":00:00Z", end: "2026-10-07T" + (15 + i) + ":30:00Z" }))
  const { d, cal } = await load({ events })
  await cal.goTo("2026-10-07", "month")
  await flush()
  const cell = d.querySelector('.cal-cell[data-cal-day="2026-10-07"]')
  assert.equal(cell.querySelectorAll("[data-cal-event]").length, 3)
  const more = cell.querySelector(".cal-more")
  assert.equal(more.textContent, "+2 more")
  more.click()
  await flush()
  assert.equal(cal.state.view, "day")
  assert.equal(cal.state.anchor, "2026-10-07")
})

test("agenda groups events by day", async () => {
  const events = [ev({ id: 1, title: "A" }), ev({ id: 2, title: "B", start: "2026-10-08T16:00:00Z", end: "2026-10-08T17:00:00Z" })]
  const { d, cal } = await load({ events })
  await cal.goTo("2026-10-07", "agenda")
  await flush()
  assert.deepEqual([...d.querySelectorAll(".cal-agenda-day")].map((n) => n.getAttribute("data-cal-day")), ["2026-10-07", "2026-10-08"])
})

test("needs_reconnect shows a banner with the reconnect form", async () => {
  const calendars = { accounts: [{ account_id: "acc1", email: "bob@example.com", needs_reconnect: true, calendars: [] }] }
  const { d } = await load({ calendars })
  const banner = d.querySelector("[data-cal-banner]")
  assert.ok(!banner.classList.contains("hidden"))
  assert.match(banner.textContent, /Reconnect bob@example\.com to show its calendar/)
  const form = banner.querySelector("form")
  assert.equal(form.getAttribute("action"), "/api/accounts/oauth2/authorize")
  assert.equal(form.method.toLowerCase(), "post")
  const f = Object.fromEntries([...form.querySelectorAll("input")].map((i) => [i.name, i.value]))
  assert.deepEqual(f, { flow_action: "reconnect", provider: "gmail", email_address: "bob@example.com", display_name: "Ann Example" })
})

test("calendar list groups by account and toggling on selects then syncs", async () => {
  const calendars = { accounts: [{ account_id: "acc1", email: "ann@example.com", needs_reconnect: false, last_synced_at: "2026-10-01T00:00:00Z",
    calendars: [{ id: 10, name: "Work", color: "#3366cc", selected: false }, { id: 11, name: "Home", color: "#cc3333", selected: true }] }] }
  const { d, calls } = await load({ calendars })
  assert.equal(d.querySelector(".cal-list-account-name").textContent, "ann@example.com")
  const boxes = [...d.querySelectorAll("[data-cal-toggle]")]
  assert.deepEqual(boxes.map((b) => b.checked), [false, true])
  const before = calls.length
  boxes[0].checked = true
  boxes[0].dispatchEvent(new d.defaultView.Event("change", { bubbles: true }))
  await flush()
  const after = calls.slice(before)
  assert.equal(after[0].url, "/api/calendar/calendars/10/selected")
  assert.equal(after[0].method, "POST")
  assert.equal(after[0].body, "selected=1")
  assert.ok(after.some((c) => c.url === "/api/calendar/sync" && c.method === "POST"))
  // events are refetched after the sync
  assert.ok(after.findIndex((c) => c.url.indexOf("/api/calendar/events") === 0) > after.findIndex((c) => c.url === "/api/calendar/sync"))
})

test("refresh button syncs and refetches", async () => {
  const { d, calls } = await load({})
  const before = calls.length
  d.querySelector("[data-cal-refresh]").click()
  await flush()
  const urls = calls.slice(before).map((c) => c.method + " " + c.url.split("?")[0])
  assert.deepEqual(urls.slice(0, 2), ["POST /api/calendar/sync", "GET /api/calendar/calendars"])
  assert.equal(urls[urls.length - 1], "GET /api/calendar/events")
})

test("keyboard: t/j/k/arrows/d/w/m/a work; ignored while typing; stop mail shortcuts", async () => {
  const { w, d, cal } = await load({})
  const mailSaw = []
  d.addEventListener("keydown", (e) => mailSaw.push(e.key))
  await cal.goTo("2026-10-07", "week")
  key(w, d.body, "j"); await flush()
  assert.equal(cal.state.anchor, "2026-10-14")
  key(w, d.body, "k"); key(w, d.body, "ArrowLeft"); await flush()
  assert.equal(cal.state.anchor, "2026-09-30")
  key(w, d.body, "ArrowRight"); await flush()
  assert.equal(cal.state.anchor, "2026-10-07")
  key(w, d.body, "m"); assert.equal(cal.state.view, "month")
  key(w, d.body, "j"); await flush()
  assert.equal(cal.state.anchor, "2026-11-07")
  key(w, d.body, "d"); assert.equal(cal.state.view, "day")
  key(w, d.body, "a"); assert.equal(cal.state.view, "agenda")
  key(w, d.body, "w"); assert.equal(cal.state.view, "week")
  key(w, d.body, "t"); await flush()
  assert.notEqual(cal.state.anchor, "2026-11-07")
  assert.deepEqual(mailSaw, [], "handled keys never reach the mail shortcuts")

  const input = d.createElement("input")
  d.body.appendChild(input)
  const before = cal.state.view
  key(w, input, "m")
  assert.equal(cal.state.view, before)
  assert.deepEqual(mailSaw, ["m"], "typing keys pass through untouched")
  key(w, d.body, "m", { ctrlKey: true })
  assert.equal(cal.state.view, before)
})

test("shortcuts do nothing when the calendar app is not on the page", async () => {
  const { w, d, cal } = await load({ body: "<div id=\"mail-list\"></div>" })
  const seen = []
  d.addEventListener("keydown", (e) => seen.push(e.key))
  key(w, d.body, "w")
  key(w, d.body, "j")
  assert.deepEqual(seen, ["w", "j"])
  assert.equal(cal.state.root, null)
})
