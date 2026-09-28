// Raven additions that live outside upstream Gofer's app.js.
// Command palette: Cmd/Ctrl+K opens a filterable list of actions and folders.
(function () {
  if (window.RavenPalette) return

  var dialog, input, list, items = [], filtered = [], active = 0

  function sidebarLink(selector) {
    return document.querySelector(selector)
  }

  function go(selector, href) {
    var link = sidebarLink(selector)
    if (link) link.click()
    else window.location.href = href
  }

  function collectItems() {
    var out = [
      { label: "Compose new email", hint: "C", run: function () { if (typeof openNewCompose === "function") openNewCompose(); else window.location.href = "/" } },
      { label: "Search mail", hint: "/", run: function () {
        var search = document.querySelector("[data-mail-search-input]")
        if (search) { search.focus(); search.select() } else window.location.href = "/"
      } },
      { label: "Contacts", run: function () { go('aside a[href="/contacts"]', "/contacts") } },
      { label: "Settings", run: function () { go('a[href="/settings/accounts"]', "/settings/accounts") } },
      { label: "Toggle light / dark", run: toggleMode },
      { label: "Keyboard shortcuts", hint: "?", run: function () {
        document.dispatchEvent(new KeyboardEvent("keydown", { key: "?", bubbles: true }))
      } }
    ]
    var seen = {}
    var links = document.querySelectorAll('aside a[hx-get^="/folder/"]')
    for (var i = 0; i < links.length; i++) {
      var link = links[i]
      var name = link.querySelector("span.truncate")
      if (!name) continue
      var group = link.closest("[data-sidebar-account]")
      var groupLabel = group && group.querySelector("[data-sidebar-account-toggle] span.truncate")
      var label = name.textContent.trim() + (groupLabel ? " — " + groupLabel.textContent.trim() : "")
      var key = link.getAttribute("hx-get")
      if (seen[key]) continue
      seen[key] = true
      out.push({ label: label, hint: "Folder", run: (function (el) { return function () { el.click() } })(link) })
    }
    return out
  }

  function toggleMode() {
    var dark = !document.documentElement.classList.contains("dark")
    if (window.GoferSettings) GoferSettings.set("theme", dark ? "dark" : "light")
    else document.documentElement.classList.toggle("dark", dark)
  }

  function build() {
    dialog = document.createElement("dialog")
    dialog.className = "raven-palette"
    dialog.setAttribute("aria-label", "Command palette")
    dialog.innerHTML =
      '<input class="raven-palette-input" type="text" placeholder="Type a command or folder…" aria-label="Command" autocomplete="off" spellcheck="false">' +
      '<ul class="raven-palette-list" role="listbox"></ul>'
    document.body.appendChild(dialog)
    input = dialog.querySelector("input")
    list = dialog.querySelector("ul")

    input.addEventListener("input", render)
    input.addEventListener("keydown", function (e) {
      if (e.key === "ArrowDown") { e.preventDefault(); move(1) }
      else if (e.key === "ArrowUp") { e.preventDefault(); move(-1) }
      else if (e.key === "Enter") { e.preventDefault(); choose(active) }
    })
    list.addEventListener("click", function (e) {
      var li = e.target.closest("li[data-index]")
      if (li) choose(Number(li.dataset.index))
    })
    dialog.addEventListener("click", function (e) {
      if (e.target === dialog) dialog.close()
    })
  }

  function render() {
    var q = input.value.trim().toLowerCase()
    filtered = items.filter(function (item) {
      return !q || item.label.toLowerCase().indexOf(q) !== -1
    })
    active = 0
    list.innerHTML = ""
    if (!filtered.length) {
      var empty = document.createElement("li")
      empty.className = "raven-palette-empty"
      empty.textContent = "No matches"
      list.appendChild(empty)
      return
    }
    filtered.forEach(function (item, i) {
      var li = document.createElement("li")
      li.dataset.index = i
      li.setAttribute("role", "option")
      var label = document.createElement("span")
      label.textContent = item.label
      li.appendChild(label)
      if (item.hint) {
        var hint = document.createElement("kbd")
        hint.textContent = item.hint
        li.appendChild(hint)
      }
      list.appendChild(li)
    })
    highlight()
  }

  function move(delta) {
    if (!filtered.length) return
    active = (active + delta + filtered.length) % filtered.length
    highlight()
  }

  function highlight() {
    var lis = list.querySelectorAll("li[data-index]")
    for (var i = 0; i < lis.length; i++) {
      var on = i === active
      lis[i].toggleAttribute("data-active", on)
      lis[i].setAttribute("aria-selected", on ? "true" : "false")
      if (on) lis[i].scrollIntoView({ block: "nearest" })
    }
  }

  function choose(index) {
    var item = filtered[index]
    if (!item) return
    dialog.close()
    item.run()
  }

  function open() {
    if (!dialog) build()
    items = collectItems()
    input.value = ""
    render()
    dialog.showModal()
    input.focus()
  }

  document.addEventListener("keydown", function (e) {
    if (!(e.metaKey || e.ctrlKey) || e.altKey || e.shiftKey || String(e.key).toLowerCase() !== "k") return
    // Cmd/Ctrl+K inside the compose editor inserts a link (app.js); leave it alone.
    if (e.target && e.target.closest && e.target.closest("[data-compose-editor]")) return
    e.preventDefault()
    if (dialog && dialog.open) dialog.close()
    else open()
  })

  window.RavenPalette = { open: open }
})()
