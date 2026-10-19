// Every action a template names must be in actions.js's allowlist, or the click does nothing.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")

const root = path.join(__dirname, "..", "..")

function templFiles(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap(function (e) {
    const p = path.join(dir, e.name)
    return e.isDirectory() ? templFiles(p) : (p.endsWith(".templ") ? [p] : [])
  })
}

test("template actions are allowlisted", () => {
  const actions = fs.readFileSync(path.join(root, "assets", "js", "actions.js"), "utf8")
  const list = actions.slice(actions.indexOf("var actionNames = ["), actions.indexOf("]", actions.indexOf("var actionNames = [")))
  const allowed = new Set(Array.from(list.matchAll(/"([^"]+)"/g), function (m) { return m[1] }))
  const missing = []
  for (const file of templFiles(path.join(root, "internal", "views")).concat(templFiles(path.join(root, "components")))) {
    const src = fs.readFileSync(file, "utf8")
    for (const m of src.matchAll(/onAction\("([^"]+)"|data-on-[a-z-]+='\["([^"]+)"/g)) {
      const name = m[1] || m[2]
      if (!allowed.has(name)) missing.push(name + " (" + path.relative(root, file) + ")")
    }
  }
  assert.deepEqual(missing, [])
})
