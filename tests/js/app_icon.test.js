// App icon: the tab and iOS home-screen icons follow the chosen style and Raven's own theme (not the
// OS), switches live, and the desktop shell is told through set_app_icon.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const root = path.join(__dirname, "..", "..")
const uiSettings = fs.readFileSync(path.join(root, "assets", "js", "ui-settings.js"), "utf8")
const settingsPage = fs.readFileSync(path.join(root, "assets", "js", "settings-page.js"), "utf8")

function page(stored, { dark = true, tauri } = {}) {
  const settings = Object.assign({ timezone: "UTC", theme: dark ? "dark" : "light" }, stored)
  const dom = new JSDOM(
    '<!doctype html><html class="' + (dark ? "dark" : "") + '"><head><link rel="icon" type="image/svg+xml" href="/assets/brand/logo-raven.svg"><link rel="apple-touch-icon" href="/assets/brand/touch-logo-raven.png"></head>' +
      "<body data-ui-settings='" + JSON.stringify(settings) + "'></body></html>",
    { url: "http://localhost/", runScripts: "outside-only" },
  )
  const w = dom.window
  w.fetch = () => Promise.resolve({ json: () => Promise.resolve({}) })
  const calls = []
  if (tauri) w.__TAURI__ = { core: { invoke: (cmd, args) => { calls.push([cmd, JSON.parse(JSON.stringify(args))]); return tauri(cmd, args) } } }
  w.eval(uiSettings)
  return {
    w,
    calls,
    icon: () => w.document.querySelector('link[rel="icon"]').getAttribute("href"),
    touch: () => w.document.querySelector('link[rel="apple-touch-icon"]').getAttribute("href"),
  }
}

const DARK = { raven: "logo-raven", classic: "logo-classic", minimal: "logo-minimal-dark" }
const LIGHT = { raven: "logo-raven-light", classic: "logo-classic-light", minimal: "logo-minimal-light" }

test("every style and appearance maps to a shipped SVG and PNG", () => {
  for (const file of [...Object.values(DARK), ...Object.values(LIGHT)]) {
    for (const ext of ["svg", "png"]) assert.ok(fs.existsSync(path.join(root, "assets", "brand", file + "." + ext)), file + "." + ext)
    assert.ok(fs.existsSync(path.join(root, "assets", "brand", "touch-" + file + ".png")), "touch-" + file + ".png")
  }
})

test("the tab icon picks the style's variant for the current theme", () => {
  for (const style of Object.keys(DARK)) {
    assert.equal(page({ app_icon: style }, { dark: true }).icon(), "/assets/brand/" + DARK[style] + ".svg")
    assert.equal(page({ app_icon: style }, { dark: false }).icon(), "/assets/brand/" + LIGHT[style] + ".svg")
  }
})

test("the iOS home-screen icon follows the same style and theme, live", () => {
  for (const style of Object.keys(DARK)) {
    assert.equal(page({ app_icon: style }, { dark: true }).touch(), "/assets/brand/touch-" + DARK[style] + ".png")
    assert.equal(page({ app_icon: style }, { dark: false }).touch(), "/assets/brand/touch-" + LIGHT[style] + ".png")
  }
  const { w, touch } = page({ app_icon: "raven" }, { dark: false })
  w.RavenSettings.set("app_icon", "minimal")
  assert.equal(touch(), "/assets/brand/touch-logo-minimal-light.png")
  w.RavenSettings.set("theme", "dark")
  assert.equal(touch(), "/assets/brand/touch-logo-minimal-dark.png")
  assert.equal(w.document.querySelectorAll('link[rel="apple-touch-icon"]').length, 1)
})

test("a missing or unknown style falls back to raven", () => {
  assert.equal(page({}).icon(), "/assets/brand/logo-raven.svg")
  assert.equal(page({ app_icon: "bogus" }, { dark: false }).icon(), "/assets/brand/logo-raven-light.svg")
})

test("changing the style or the theme swaps the tab icon without a reload", () => {
  const { w, icon } = page({ app_icon: "raven" }, { dark: true })
  w.RavenSettings.set("app_icon", "classic")
  assert.equal(icon(), "/assets/brand/logo-classic.svg")
  w.RavenSettings.set("theme", "light")
  assert.equal(icon(), "/assets/brand/logo-classic-light.svg")
  w.RavenSettings.set("app_icon", "minimal")
  assert.equal(icon(), "/assets/brand/logo-minimal-light.svg")
  w.RavenSettings.set("theme", "dark")
  assert.equal(icon(), "/assets/brand/logo-minimal-dark.svg")
  assert.equal(w.document.querySelectorAll('link[rel="icon"]').length, 1)
})

test("the OS color scheme is not consulted: Raven's theme decides", () => {
  assert.doesNotMatch(uiSettings.match(/function applyAppIcon[\s\S]*?\n  }\n/)[0], /prefers-color-scheme|matchMedia/)
})

test("the desktop shell is told the style and appearance on load and on change", () => {
  const { w, calls } = page({ app_icon: "classic" }, { dark: true, tauri: () => Promise.resolve() })
  assert.deepEqual(calls, [["set_app_icon", { style: "classic", dark: true }]])
  w.RavenSettings.set("app_icon", "minimal")
  w.RavenSettings.set("theme", "light")
  w.RavenSettings.set("theme", "light")
  assert.deepEqual(calls.slice(1), [
    ["set_app_icon", { style: "minimal", dark: true }],
    ["set_app_icon", { style: "minimal", dark: false }],
  ])
})

test("a failing invoke does not break the page and is retried on the next change", async () => {
  const { w, calls } = page({}, { tauri: () => Promise.reject(new Error("denied")) })
  await new Promise((r) => setTimeout(r, 0))
  w.RavenSettings.set("app_icon", "raven")
  assert.equal(calls.length, 2)
})

test("the browser build never touches Tauri", () => {
  assert.doesNotThrow(() => page({ app_icon: "minimal" }))
})

test("the settings page saves a picked tile through RavenSettings", () => {
  // settings-page.js is not loadable in a bare window (it wires the whole settings page),
  // so check the delegated change handler by source, next to the theme_style one it copies.
  assert.match(settingsPage, /closest\('input\[name="app_icon"\]'\)[\s\S]*?RavenSettings\.set\('app_icon', appIcon\.value\)/)
})
