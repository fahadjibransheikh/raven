// Dark body mode: the reader's script (buildDarkModeScript in internal/handler/handler.go) lifts
// dark text colours off the dark page. It judges computed styles, which a dark-mode browser
// extension can rewrite while the frame loads and put back afterwards, so the script has to
// look again once the document has loaded.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const go = fs.readFileSync(path.join(__dirname, "..", "..", "internal", "handler", "handler.go"), "utf8")
const fn = go.slice(go.indexOf("func buildDarkModeScript"), go.indexOf("func buildLightModeScript"))
const script = fn
  .slice(fn.indexOf("<script>") + 8, fn.indexOf("</script>"))
  .replace("var db=%q,df=%q;", 'var db="#161618",df="#ececee";')

const BODY = '<div id="t" style="color:#000">Hey team</div><div id="u" style="color:rgb(34,34,34)">Hello</div>'

function load(extensionRewritesColors) {
  const dom = new JSDOM("<!doctype html><body>" + BODY + "</body>", { runScripts: "outside-only" })
  const w = dom.window
  let loading = true
  if (extensionRewritesColors) {
    // While loading, report every text colour as light, as a dark-mode extension's stylesheet does.
    const real = w.getComputedStyle.bind(w)
    w.getComputedStyle = (el) => {
      const cs = real(el)
      if (!loading) return cs
      return new Proxy(cs, {
        get: (t, k) => (k === "color" ? "rgb(232, 230, 227)" : typeof t[k] === "function" ? t[k].bind(t) : t[k]),
      })
    }
  }
  w.eval(script)
  loading = false
  w.dispatchEvent(new w.Event("load"))
  return w.document
}

test("dark inline text colours become light", () => {
  const d = load(false)
  for (const id of ["t", "u"]) {
    const el = d.getElementById(id)
    assert.equal(el.style.getPropertyValue("color"), "rgb(236, 236, 238)")
    assert.equal(el.style.getPropertyPriority("color"), "important")
  }
})

test("dark text is lifted even when the first pass saw rewritten colours", () => {
  const d = load(true)
  for (const id of ["t", "u"]) {
    assert.equal(d.getElementById(id).style.getPropertyValue("color"), "rgb(236, 236, 238)")
  }
})
