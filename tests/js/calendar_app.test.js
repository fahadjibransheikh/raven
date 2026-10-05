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
    calendars: [{ id: 10, account_id: "acc1", name: "Work", color: "#3366cc", selected: true, is_primary: true, access_role: "owner" }],
  }],
}

// opts: events, calendars, tz, body (default: the real shell), nowait
async function load(opts) {
  opts = opts || {}
  const dom = new JSDOM("<!doctype html><html data-timezone=\"" + (opts.tz || LA) + "\"><body>" + (opts.body === undefined ? shell : opts.body) + "</body></html>",
    { url: "http://localhost/calendar" + (opts.search || ""), runScripts: "outside-only", pretendToBeVisual: true })
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

test("outlook accounts reconnect as outlook and get outlook wording", async () => {
  const calendars = { accounts: [
    { account_id: "acc2", email: "pat@outlook.com", provider: "outlook", needs_reconnect: true, calendars: [] },
    { account_id: "acc1", email: "ann@example.com", provider: "gmail", needs_reconnect: false, last_synced_at: "2026-10-01T00:00:00Z",
      calendars: [{ id: 10, account_id: "acc1", provider: "gmail", name: "Work", color: "#3366cc", selected: true, is_primary: true, access_role: "owner" }] },
    { account_id: "acc3", email: "pat@msn.com", provider: "outlook", needs_reconnect: false, last_synced_at: "2026-10-01T00:00:00Z",
      calendars: [{ id: 30, account_id: "acc3", provider: "outlook", name: "Calendar", color: "#0078d4", selected: true, is_primary: true, access_role: "owner" }] },
  ] }
  const events = [ev({ id: 7, calendar_id: 30, account_id: "acc3", html_link: "https://outlook.live.com/calendar/item/x" })]
  const { w, d, cal } = await load({ calendars, events })
  const f = Object.fromEntries([...d.querySelector("[data-cal-banner] form").querySelectorAll("input")].map((i) => [i.name, i.value]))
  assert.equal(f.provider, "outlook")
  await cal.goTo("2026-10-07", "week")
  await flush()
  d.querySelector("[data-cal-event]").click()
  const pop = d.querySelector("[data-cal-popover]")
  assert.ok([...pop.querySelectorAll("a")].some((a) => a.textContent === "Open in Outlook"))
  assert.ok(![...pop.querySelectorAll("a")].some((a) => /Google/.test(a.textContent)))
  key(w, d.body, "Escape")
  key(w, d.body, "c")
  const sel = d.getElementById("cal-ed-calendar")
  const label = () => d.getElementById("cal-ed-meet").closest("label").textContent
  sel.value = "30"
  sel.dispatchEvent(new w.Event("change", { bubbles: true }))
  assert.equal(label(), "Add online meeting")
  sel.value = "10"
  sel.dispatchEvent(new w.Event("change", { bubbles: true }))
  assert.equal(label(), "Add Google Meet video conferencing")
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

// ---------- C2: editing ----------

const writes = (calls) => calls.filter((c) => c.method !== "GET" && c.url !== "/api/calendar/sync" && !/\/selected$/.test(c.url))
const params = (c) => Object.fromEntries(new URLSearchParams(c.body))
const click = (w, el, init) => el.dispatchEvent(new w.MouseEvent("click", Object.assign({ bubbles: true, cancelable: true }, init)))
const field = (d, id) => d.getElementById("cal-ed-" + id)

for (const [tz, offset] of [[LA, "-07:00"], ["Asia/Kolkata", "+05:30"]]) {
  test("clicking an empty slot opens the editor prefilled in " + tz + " and saves with that offset", async () => {
    const { w, d, cal, calls } = await load({ tz })
    await cal.goTo("2026-10-07", "week")
    await flush()
    const col = d.querySelector('.cal-daycol[data-cal-day="2026-10-07"]')
    col.getBoundingClientRect = () => ({ top: 0, left: 0, right: 100, bottom: 1152, width: 100, height: 1152 })
    click(w, col, { clientY: 10.6 * 48 }) // 10:36 -> the 10:30 half hour
    assert.ok(d.querySelector("[data-cal-editor]"))
    assert.equal(field(d, "sd").value, "2026-10-07")
    assert.equal(field(d, "st").value, "10:30")
    assert.equal(field(d, "et").value, "11:30")
    assert.equal(field(d, "calendar").value, "10")
    assert.ok(field(d, "tz").closest(".cal-ed-row").classList.contains("hidden"), "zone hidden when it is the user's")
    field(d, "title").value = "Lunch"
    d.querySelector("[data-cal-editor-save]").click()
    await flush()
    const [post] = writes(calls)
    assert.equal(post.method, "POST")
    assert.equal(post.url, "/api/calendar/events")
    const p = params(post)
    assert.equal(p.start, "2026-10-07T10:30:00" + offset)
    assert.equal(p.end, "2026-10-07T11:30:00" + offset)
    assert.equal(p.time_zone, tz)
    assert.equal(p.calendar_id, "10")
    assert.equal(p.title, "Lunch")
    assert.equal(d.querySelector("[data-cal-editor]"), null, "closed after saving")
    assert.ok(eventsCalls(calls).length >= 2, "range reloaded after the write")
  })
}

test("a 1-day all-day event is sent with an exclusive end date", async () => {
  const { w, d, cal, calls } = await load({})
  await cal.goTo("2026-10-07", "month")
  await flush()
  click(w, d.querySelector('.cal-cell[data-cal-day="2026-10-08"]'))
  assert.ok(field(d, "allday").checked)
  assert.equal(field(d, "sd").value, "2026-10-08")
  assert.equal(field(d, "ed").value, "2026-10-08", "the form shows the inclusive last day")
  d.querySelector("[data-cal-editor-save]").click()
  await flush()
  let p = params(writes(calls)[0])
  assert.equal(p.all_day, "1")
  assert.equal(p.start_date, "2026-10-08")
  assert.equal(p.end_date, "2026-10-09")
  assert.equal(p.start, undefined)
  // Three days: Oct 8..10 inclusive -> end 11 exclusive. Moving the start keeps the length.
  click(w, d.querySelector('.cal-cell[data-cal-day="2026-10-08"]'))
  field(d, "ed").value = "2026-10-10"
  field(d, "ed").dispatchEvent(new w.Event("change", { bubbles: true }))
  field(d, "sd").value = "2026-10-12"
  field(d, "sd").dispatchEvent(new w.Event("change", { bubbles: true }))
  assert.equal(field(d, "ed").value, "2026-10-14")
  d.querySelector("[data-cal-editor-save]").click()
  await flush()
  p = params(writes(calls)[1])
  assert.deepEqual([p.start_date, p.end_date], ["2026-10-12", "2026-10-15"])
})

test("editor validates the end, collects guest chips and Enter in the title saves", async () => {
  const { w, d, cal, calls } = await load({})
  await cal.goTo("2026-10-07", "week")
  key(w, d.body, "c")
  field(d, "et").value = "09:00"
  field(d, "st").value = "10:00"
  d.querySelector("[data-cal-editor-save]").click()
  await flush()
  assert.equal(writes(calls).length, 0)
  assert.match(d.querySelector(".cal-ed-error").textContent, /End must be after/)
  field(d, "et").value = "23:00"
  const g = field(d, "guests")
  g.value = "a@example.com, nope"
  key(w, g, "Enter")
  assert.deepEqual([...d.querySelectorAll(".cal-chip > span")].map((n) => n.textContent), ["a@example.com"])
  assert.equal(g.value, "nope")
  g.value = "b@example.com"
  key(w, g, "Enter")
  field(d, "meet").checked = true
  field(d, "repeat").value = "weekly"
  key(w, field(d, "title"), "Enter")
  await flush()
  const p = params(writes(calls)[0])
  assert.equal(p.attendees, "a@example.com,b@example.com")
  assert.equal(p.add_meet, "1")
  assert.equal(p.recurrence, "weekly")
})

test("recurring delete asks which events; non-recurring asks for confirmation", async () => {
  const events = [ev({ id: 1, title: "Weekly", recurring_event_id: "rec" }), ev({ id: 2, title: "Once", start: "2026-10-08T16:00:00Z", end: "2026-10-08T17:00:00Z" })]
  const { w, d, cal, calls } = await load({ events })
  await cal.goTo("2026-10-07", "week")
  await flush()
  d.querySelector('[data-cal-event="1"]').click()
  d.querySelector("[data-cal-delete]").click()
  const prompt = d.querySelector("[data-cal-scope]")
  assert.ok(prompt)
  assert.deepEqual([...prompt.querySelectorAll("[data-cal-scope-choice]")].map((b) => b.textContent), ["This event", "All events"])
  assert.equal(writes(calls).length, 0, "nothing is deleted before the choice")
  key(w, d.body, "Escape")
  assert.equal(d.querySelector("[data-cal-scope]"), null, "Esc cancels")
  assert.equal(writes(calls).length, 0)
  d.querySelector("[data-cal-delete]").click()
  d.querySelector('[data-cal-scope-choice="series"]').click()
  await flush()
  assert.deepEqual(writes(calls).map((c) => [c.method, c.url]), [["DELETE", "/api/calendar/events/1?scope=series"]])
  // Non-recurring: one confirm button, cancel does nothing.
  d.querySelector('[data-cal-event="2"]').click()
  d.querySelector("[data-cal-delete]").click()
  assert.deepEqual([...d.querySelectorAll("[data-cal-scope-choice]")].map((b) => b.textContent), ["Delete"])
  d.querySelector("[data-cal-scope-cancel]").click()
  await flush()
  assert.equal(writes(calls).length, 1)
})

test("editing a recurring event asks for scope on save and sends only what changed", async () => {
  const events = [ev({ id: 1, title: "Weekly", recurring_event_id: "rec", attendees: [{ email: "x@example.com", response: "accepted" }] })]
  const { d, cal, calls } = await load({ events })
  await cal.goTo("2026-10-07", "week")
  await flush()
  d.querySelector('[data-cal-event="1"]').click()
  d.querySelector("[data-cal-edit]").click()
  assert.equal(field(d, "title").value, "Weekly")
  assert.equal(field(d, "calendar").disabled, true)
  assert.equal(field(d, "repeat").disabled, true)
  assert.deepEqual([...d.querySelectorAll(".cal-chip > span")].map((n) => n.textContent), ["x@example.com"])
  field(d, "title").value = "Weekly sync"
  d.querySelector("[data-cal-editor-save]").click()
  assert.equal(writes(calls).length, 0)
  d.querySelector('[data-cal-scope-choice="this"]').click()
  await flush()
  const [patch] = writes(calls)
  assert.deepEqual([patch.method, patch.url], ["PATCH", "/api/calendar/events/1"])
  const p = params(patch)
  assert.equal(p.title, "Weekly sync")
  assert.equal(p.scope, "this")
  for (const k of ["start", "end", "all_day", "attendees", "recurrence", "reminder_minutes"]) assert.equal(p[k], undefined, k + " unchanged, not sent")
})

test("RSVP buttons appear only for non-organizer attendees on writable calendars", async () => {
  const me = { email: "ann@example.com", self: true, response: "needsAction" }
  const boss = { email: "boss@example.com", organizer: true, response: "accepted" }
  const events = [
    ev({ id: 1, title: "Invite", attendees: [boss, me], self_response: "needsAction" }),
    ev({ id: 2, title: "Mine", start: "2026-10-08T16:00:00Z", end: "2026-10-08T17:00:00Z", attendees: [Object.assign({}, me, { organizer: true, response: "accepted" })] }),
    ev({ id: 3, title: "Solo", start: "2026-10-09T16:00:00Z", end: "2026-10-09T17:00:00Z" }),
    ev({ id: 4, title: "Foreign", calendar_id: 11, start: "2026-10-10T16:00:00Z", end: "2026-10-10T17:00:00Z", attendees: [boss, me] }),
  ]
  const calendars = { accounts: [{ account_id: "acc1", email: "ann@example.com", last_synced_at: "2026-10-01T00:00:00Z", calendars: [
    { id: 10, name: "Work", selected: true, is_primary: true, access_role: "owner" }, { id: 11, name: "Shared", selected: true, access_role: "reader" }] }] }
  const { d, cal, calls } = await load({ events, calendars })
  await cal.goTo("2026-10-07", "week")
  await flush()
  const rsvpFor = (id) => { d.querySelector('[data-cal-event="' + id + '"]').click(); return [...d.querySelectorAll("[data-cal-rsvp]")].map((b) => b.getAttribute("data-cal-rsvp")) }
  assert.deepEqual(rsvpFor(1), ["accepted", "tentative", "declined"])
  assert.deepEqual(rsvpFor(2), [], "organizer")
  assert.deepEqual(rsvpFor(3), [], "no attendees")
  assert.deepEqual(rsvpFor(4), [], "read-only calendar")
  assert.equal(d.querySelector("[data-cal-edit]"), null, "no Edit on a read-only calendar")
  rsvpFor(1)
  d.querySelector('[data-cal-rsvp="tentative"]').click()
  await flush()
  const [post] = writes(calls)
  assert.deepEqual([post.method, post.url, params(post).response], ["POST", "/api/calendar/events/1/rsvp", "tentative"])
})

test("c opens a new event without reaching mail compose; e edits the open event; Esc closes the editor", async () => {
  const { w, d, cal, calls } = await load({ events: [ev({ id: 1, title: "Standup" })] })
  const mailSaw = []
  d.addEventListener("keydown", (e) => mailSaw.push(e.key))
  await cal.goTo("2026-10-07", "week")
  await flush()
  const e1 = key(w, d.body, "c")
  assert.ok(d.querySelector("[data-cal-editor]"), "c opens the editor")
  assert.ok(e1.defaultPrevented)
  const typed = key(w, field(d, "title"), "c")
  assert.equal(typed.defaultPrevented, false, "typing in the title is untouched")
  key(w, d.querySelector("[data-cal-editor-cancel]"), "c") // a non-typing target inside the editor: swallowed
  key(w, field(d, "title"), "Escape")
  assert.equal(d.querySelector("[data-cal-editor]"), null, "Esc closes the editor")
  assert.deepEqual(mailSaw.filter((k) => k !== "c"), [], "no stray keys")
  assert.deepEqual(mailSaw, ["c"], "only the key typed into the title input reached other listeners")
  d.querySelector('[data-cal-event="1"]').click()
  key(w, d.body, "e")
  assert.equal(field(d, "title").value, "Standup")
  assert.equal(writes(calls).length, 0)
  key(w, field(d, "title"), "Escape")
  key(w, d.body, "e") // nothing open: swallowed, no editor
  assert.equal(d.querySelector("[data-cal-editor]"), null)
  assert.deepEqual(mailSaw, ["c"], "e never reaches mail archive")
})

test("save errors keep the editor open and show the server message", async () => {
  const { w, d, cal, calls } = await load({})
  const toasts = []
  w.showGoferToast = (o) => toasts.push(o)
  const realFetch = w.fetch
  w.fetch = (url, init) => {
    if (init && init.method === "POST" && url === "/api/calendar/events") {
      return Promise.resolve({ ok: false, status: 403, json: () => Promise.resolve({ error: "read_only", message: "This calendar is read-only." }) })
    }
    return realFetch(url, init)
  }
  await cal.goTo("2026-10-07", "week")
  key(w, d.body, "c")
  d.querySelector("[data-cal-editor-save]").click()
  await flush()
  assert.ok(d.querySelector("[data-cal-editor]"))
  assert.match(d.querySelector(".cal-ed-error").textContent, /read-only/)
  assert.equal(d.querySelector("[data-cal-editor-save]").disabled, false)
  assert.equal(toasts.at(-1).variant, "error")
  assert.equal(calls.filter((c) => c.method === "POST").length, 0) // the failing fetch bypassed the recorder
})

test("?date= opens the calendar at that day (Open in Calendar link on invitation cards)", async () => {
  const { d } = await load({ search: "?date=2026-11-03" })
  assert.match(d.querySelector("[data-cal-title]").textContent, /Nov.*2026/)
  const bad = await load({ search: "?date=garbage" })
  assert.ok(bad.d.querySelector("[data-cal-body]"))
})
