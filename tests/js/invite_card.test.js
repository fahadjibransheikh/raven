// Runs assets/js/invite-card.js in jsdom against the real templ card (scripts/render-invite) with mocked endpoints.
const { test, after } = require("node:test")
const assert = require("node:assert/strict")
const { execFileSync } = require("node:child_process")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const root = path.join(__dirname, "..", "..")
const cards = JSON.parse(execFileSync("go", ["run", "./scripts/render-invite"], { cwd: root, encoding: "utf8" }))
const src = fs.readFileSync(path.join(root, "assets", "js", "invite-card.js"), "utf8")

const windows = []
after(() => windows.forEach((w) => w.close()))
const flush = async () => { for (let i = 0; i < 8; i++) await new Promise((r) => setTimeout(r, 0)) }

// routes: map of "METHOD url" -> {status, body, json}. Records every call.
async function load(routes, tz) {
  const dom = new JSDOM('<!doctype html><html data-timezone="' + (tz || "UTC") + '"><body><div data-invite-slot="42" class="invite-slot hidden"></div></body></html>',
    { url: "http://localhost/", runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  windows.push(w)
  const calls = []
  w.fetch = (url, init) => {
    const method = (init && init.method) || "GET"
    calls.push({ url, method, body: (init && init.body) || "" })
    const r = routes[method + " " + url] || { status: 404, body: "" }
    const status = r.status || 200
    return Promise.resolve({
      ok: status >= 200 && status < 300, status,
      text: () => Promise.resolve(r.body || ""), json: () => (r.json ? Promise.resolve(r.json) : Promise.reject(new Error("no json"))),
    })
  }
  w.eval(src)
  w.postMessage({ type: "emailBodyResize", emailId: "42", height: 100 }, "*")
  await flush()
  return { w, d: w.document, calls }
}

test("loads the card into the slot once the body frame reports in, and ignores repeat resizes", async () => {
  const { w, d, calls } = await load({ "GET /email/42/invite": { body: cards.found } })
  const slot = d.querySelector("[data-invite-slot]")
  assert.ok(!slot.classList.contains("hidden"))
  assert.ok(slot.querySelector("[data-invite-card]"))
  w.postMessage({ type: "emailBodyResize", emailId: "42", height: 120 }, "*")
  await flush()
  assert.equal(calls.filter((c) => c.url === "/email/42/invite").length, 1)
})

test("a message without an invite leaves the slot hidden", async () => {
  const { d } = await load({ "GET /email/42/invite": { status: 204 } })
  assert.ok(d.querySelector("[data-invite-slot]").classList.contains("hidden"))
})

test("clicking Yes posts response=accepted to the matched event id and updates the card", async () => {
  const { d, calls } = await load({
    "GET /email/42/invite": { body: cards.found },
    "POST /api/calendar/events/77/rsvp": { json: { ok: true, response: "accepted" } },
  })
  assert.equal(calls.filter((c) => c.method === "POST").length, 0, "loading the card must not RSVP by itself")
  d.querySelector('[data-invite-rsvp="accepted"]').click()
  await flush()
  const posts = calls.filter((c) => c.method === "POST")
  assert.equal(posts.length, 1)
  assert.equal(posts[0].url, "/api/calendar/events/77/rsvp")
  assert.equal(posts[0].body, "response=accepted")
  assert.equal(d.querySelector('[data-invite-rsvp="accepted"]').getAttribute("aria-pressed"), "true")
  assert.equal(d.querySelector('[data-invite-rsvp="declined"]').getAttribute("aria-pressed"), "false")
  assert.equal(d.querySelector("[data-invite-response]").textContent, "Going")
  assert.ok(d.querySelector("[data-invite-error]").classList.contains("hidden"))
})

test("a failed RSVP shows the server's message and leaves the state unchanged", async () => {
  const { d } = await load({
    "GET /email/42/invite": { body: cards.found },
    "POST /api/calendar/events/77/rsvp": { status: 400, json: { message: "you are not a guest of this event" } },
  })
  d.querySelector('[data-invite-rsvp="declined"]').click()
  await flush()
  const err = d.querySelector("[data-invite-error]")
  assert.ok(!err.classList.contains("hidden"))
  assert.match(err.textContent, /not a guest/)
  assert.equal(d.querySelector("[data-invite-response]").textContent, "Not responded")
})

test("not-found card: Sync calendar posts the sync, then re-checks and shows the buttons", async () => {
  let n = 0
  const { d, calls } = await load({
    "GET /email/42/invite": { get body() { return n++ === 0 ? cards.missing : cards.found } },
    "POST /api/calendar/sync": { json: { accounts: [] } },
  })
  assert.match(d.querySelector("[data-invite-card]").textContent, /Not in your calendar yet/)
  assert.equal(d.querySelector("[data-invite-rsvp]"), null)
  d.querySelector("[data-invite-sync]").click()
  await flush()
  assert.deepEqual(calls.map((c) => c.method + " " + c.url), ["GET /email/42/invite", "POST /api/calendar/sync", "GET /email/42/invite"])
  assert.equal(d.querySelector("[data-invite-card]").getAttribute("data-event-id"), "77")
  assert.ok(d.querySelector('[data-invite-rsvp="accepted"]'))
})

test("time is re-rendered in the user's time zone and the calendar link follows it", async () => {
  const { d } = await load({ "GET /email/42/invite": { body: cards.found } }, "Pacific/Auckland")
  const text = d.querySelector("[data-invite-when]").textContent
  assert.match(text, /Oct 8, 2026, 5:00 AM – 6:00 AM/) // 16:00Z is 05:00 NZDT the next day
  assert.equal(d.querySelector("[data-invite-open]").getAttribute("href"), "/calendar?date=2026-10-08")
})
