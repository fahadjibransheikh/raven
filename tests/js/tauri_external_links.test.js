// Calendar "Join meeting", invite cards and every other <a target="_blank"> did nothing in the desktop app.
// tauri-plugin-shell injects a body click handler that sends each http(s)/mailto/tel target=_blank click to
// invoke("plugin:shell|open") and calls preventDefault() right away. The Raven page is remote
// (http://127.0.0.1:<port>), so that invoke is rejected unless a remote capability grants shell:allow-open,
// and the click is already cancelled by then.
const { test } = require("node:test")
const assert = require("node:assert/strict")
const fs = require("node:fs")
const path = require("node:path")
const { JSDOM } = require("jsdom")

const tauri = path.join(__dirname, "..", "..", "tauri-wrapper", "src-tauri")
const libRs = fs.readFileSync(path.join(tauri, "src", "lib.rs"), "utf8")
const remote = JSON.parse(fs.readFileSync(path.join(tauri, "capabilities", "remote-notifications.json"), "utf8"))

// Same logic as tauri-plugin-shell 2.3.6 src/init-iife.js (readable form).
function installShellClickHandler(window, invoke) {
  window.document.body.addEventListener("click", function (e) {
    for (let n = e.target; n; n = n.parentElement) {
      if (n.matches("a")) {
        if (n.href !== "" && ["http://", "https://", "mailto:", "tel:"].some((p) => n.href.startsWith(p)) && n.target === "_blank") {
          invoke("plugin:shell|open", { path: n.href })
          e.preventDefault()
        }
        break
      }
    }
  })
}

test("the shell plugin cancels target=_blank clicks, so the remote page must be allowed to open them", () => {
  assert.match(libRs, /tauri_plugin_shell::init\(\)/, "shell plugin (and its click handler) is registered")

  const dom = new JSDOM('<body><a id="join" href="https://meet.google.com/abc-defg-hij" target="_blank" rel="noopener noreferrer"><svg></svg></a></body>')
  installShellClickHandler(dom.window, () => {})
  const click = new dom.window.MouseEvent("click", { bubbles: true, cancelable: true })
  dom.window.document.querySelector("svg").dispatchEvent(click)
  assert.equal(click.defaultPrevented, true, "the plugin handler takes over the click")

  assert.deepEqual(remote.remote.urls, ["http://127.0.0.1:*"])
  assert.ok(remote.permissions.includes("shell:allow-open"), "remote capability grants shell:allow-open")
})
