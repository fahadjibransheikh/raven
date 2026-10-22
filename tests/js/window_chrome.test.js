// Desktop window theme: an explicit Light or Dark pins the native window to it, while System
// unpins it (setTheme(null)) so the page's prefers-color-scheme keeps following the OS.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const root = path.join(__dirname, "..", "..")
const chrome = fs.readFileSync(path.join(root, "tauri-wrapper", "src-tauri", "src", "window_chrome.js"), "utf8")
const actions = fs.readFileSync(path.join(root, "assets", "js", "actions.js"), "utf8")

function page(mode, { osDark = true } = {}) {
  const dom = new JSDOM('<!doctype html><html data-theme-mode="' + mode + '" class="' + (mode === "dark" ? "dark" : "") + '"><body></body></html>', {
    url: "http://127.0.0.1:8090/",
    runScripts: "outside-only",
  })
  const w = dom.window
  const listeners = []
  const media = { matches: osDark, addEventListener: (_, fn) => listeners.push(fn) }
  w.matchMedia = () => media
  const themes = []
  w.__TAURI__ = { window: { getCurrentWindow: () => ({ setTheme: (t) => { themes.push(t); return Promise.resolve() } }) } }
  w.eval(actions)
  w.eval(chrome)
  const osChange = (dark) => { media.matches = dark; listeners.forEach((fn) => fn({ matches: dark })) }
  return { w, html: w.document.documentElement, themes, osChange }
}

const tick = () => new Promise((r) => setTimeout(r, 0))

test("System mode unpins the window instead of forcing the current theme", async () => {
  const { html, themes } = page("system", { osDark: true })
  await tick()
  assert.equal(JSON.stringify(themes), JSON.stringify([null]))
  assert.ok(html.classList.contains("dark"))
})

test("an explicit mode pins the window to it", async () => {
  const dark = page("dark")
  const light = page("light")
  await tick()
  assert.equal(JSON.stringify(dark.themes), JSON.stringify(["dark"]))
  assert.equal(JSON.stringify(light.themes), JSON.stringify(["light"]))
})

test("System mode follows an OS change after load", async () => {
  const { html, themes, osChange } = page("system", { osDark: false })
  await tick()
  assert.ok(!html.classList.contains("dark"))
  osChange(true)
  assert.ok(html.classList.contains("dark"))
  assert.equal(JSON.stringify(themes), JSON.stringify([null]))
})

test("switching from Light to System unpins, and the OS change that follows applies", async () => {
  const { html, themes, osChange } = page("light", { osDark: true })
  await tick()
  html.setAttribute("data-theme-mode", "system")
  await tick()
  assert.equal(JSON.stringify(themes), JSON.stringify(["light", null]))
  osChange(true)
  assert.ok(html.classList.contains("dark"))
})
