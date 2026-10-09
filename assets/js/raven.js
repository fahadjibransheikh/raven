// Raven additions that live outside upstream Gofer's app.js.
// Command palette: Cmd/Ctrl+K opens a filterable list of actions and folders.
(function () {
  if (window.RavenPalette) return

  var dialog, input, list, items = [], filtered = [], active = 0

  // Key hints read Cmd on Mac and Ctrl elsewhere; CSS swaps them off this attribute.
  var platform = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || ""
  document.documentElement.setAttribute("data-platform", /mac|iphone|ipad/i.test(platform) ? "mac" : "other")

  function sidebarLink(selector) {
    return document.querySelector(selector)
  }

  function go(selector, href) {
    var link = sidebarLink(selector)
    if (link) link.click()
    else window.location.href = href
  }

  var GROUP_ORDER = ["Message actions", "Go to", "App"]

  // Rows for registry entries (window.RavenShortcuts, app.js). Each row runs the registry's own
  // run(), the same function its key calls. Selection-only entries appear only with a message in play.
  function shortcutItems() {
    var reg = window.RavenShortcuts
    if (!reg) return []
    var inPlay = reg.hasMailInPlay()
    return reg.list().filter(function (sc) {
      return sc.palette && (!sc.needsMessage || inPlay)
    }).map(function (sc) {
      var k = sc.keys[0]
      return { group: sc.group === "Message" ? "Message actions" : "App", label: sc.label, hint: k.length === 1 ? k.toUpperCase() : k, run: sc.run }
    })
  }

  function collectItems() {
    var out = shortcutItems()
    out.push(
      { group: "Go to", label: "Mail", run: function () { go('a[data-sidebar-app-button="mail"]', "/") } },
      { group: "Go to", label: "Contacts", run: function () { go('a[data-sidebar-app-button="contacts"]', "/contacts") } },
      { group: "Go to", label: "Calendar", run: function () { go('a[data-sidebar-app-button="calendar"]', "/calendar") } },
      { group: "Go to", label: "Settings", run: function () { go('a[href="/settings/accounts"]', "/settings/accounts") } },
      { group: "App", label: "Toggle light / dark", run: toggleMode },
      { group: "App", label: "What's new", run: function () { if (window.RavenWhatsNew) window.RavenWhatsNew.open() } }
    )
    // Folder links stay in the DOM when their account section is collapsed, so this finds them all.
    var seen = {}
    var links = document.querySelectorAll('aside a[hx-get^="/folder/"]')
    for (var i = 0; i < links.length; i++) {
      var link = links[i]
      var name = link.querySelector("span.truncate")
      if (!name) continue
      var group = link.closest("[data-sidebar-account]")
      var groupLabel = group && group.querySelector("[data-sidebar-account-toggle] span.truncate")
      var label = name.textContent.trim() + (groupLabel ? " \u2014 " + groupLabel.textContent.trim() : "")
      var key = link.getAttribute("hx-get")
      if (seen[key]) continue
      seen[key] = true
      out.push({ group: "Go to", label: label, run: (function (el) { return function () { el.click() } })(link) })
    }
    return out
  }

  // Case-insensitive subsequence match. Lower is better: 0 prefix, 1 word start,
  // 2 substring, 3 scattered letters (tighter spans first); -1 no match.
  function matchScore(label, q) {
    label = label.toLowerCase()
    if (label.indexOf(q) === 0) return 0
    var at = label.indexOf(q)
    while (at > 0 && /[a-z0-9]/.test(label.charAt(at - 1))) at = label.indexOf(q, at + 1)
    if (at > 0) return 1 + at / 1000
    if (label.indexOf(q) !== -1) return 2
    var pos = -1, first = -1
    for (var i = 0; i < q.length; i++) {
      pos = label.indexOf(q.charAt(i), pos + 1)
      if (pos === -1) return -1
      if (first === -1) first = pos
    }
    return 3 + (pos - first) / 1000
  }

  function filterItems(all, q) {
    q = q.trim().toLowerCase().replace(/\s+/g, " ")
    var rows = []
    all.forEach(function (item, i) {
      var s = q ? matchScore(item.label, q) : 0
      if (s >= 0) rows.push({ item: item, s: s, i: i, g: GROUP_ORDER.indexOf(item.group) })
    })
    rows.sort(function (a, b) { return (a.g - b.g) || (a.s - b.s) || (a.i - b.i) })
    return rows.map(function (r) { return r.item })
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
      '<input class="raven-palette-input" type="text" placeholder="Search or jump to…" aria-label="Command" autocomplete="off" spellcheck="false">' +
      '<ul class="raven-palette-list" role="listbox"></ul>' +
      '<div class="raven-palette-footer"><span><kbd>↑</kbd><kbd>↓</kbd> Select</span><span><kbd>↵</kbd> Open</span><span><kbd>Esc</kbd> Close</span></div>'
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
    filtered = filterItems(items, input.value)
    active = 0
    list.innerHTML = ""
    if (!filtered.length) {
      var empty = document.createElement("li")
      empty.className = "raven-palette-empty"
      empty.textContent = "No matches"
      list.appendChild(empty)
      return
    }
    var lastGroup = ""
    filtered.forEach(function (item, i) {
      if (item.group && item.group !== lastGroup) {
        lastGroup = item.group
        var heading = document.createElement("li")
        heading.className = "raven-palette-group"
        heading.setAttribute("role", "presentation")
        heading.textContent = item.group
        list.appendChild(heading)
      }
      var li = document.createElement("li")
      li.dataset.index = i
      li.setAttribute("role", "option")
      var label = document.createElement("span")
      label.textContent = item.label
      li.appendChild(label)
      if (item.hint) {
        var hint = document.createElement("kbd")
        hint.className = "kbd"
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

  // custom = { label, items } swaps the command list for a one-off picker
  // (used by "Move to..."); omit it for the normal command palette.
  function open(custom) {
    if (!dialog) build()
    items = custom && custom.items ? custom.items : collectItems()
    input.placeholder = custom && custom.label ? custom.label : "Type a command or folder\u2026"
    dialog.setAttribute("aria-label", custom && custom.label ? custom.label : "Command palette")
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

  window.RavenPalette = { open: open, filter: filterItems, items: collectItems, pick: function (label, pickItems) { open({ label: label, items: pickItems }) } }
})();

// Confirm prompts: window.confirm returns false without showing anything in the
// desktop app's WKWebView, so every confirmation goes through an in-app popover.
(function () {
  if (window.goferChoice) return

  // Resolves the chosen action, or "cancel" when dismissed. Mounted inside the
  // open modal dialog, if any, so it isn't inert.
  // options.input = { value, placeholder } adds a text field; its final text is written back to
  // options.input.value (used by goferPrompt).
  window.goferChoice = function (title, text, actions, options) {
    return new Promise(function (resolve) {
      var panel = document.createElement("div")
      panel.className = "compose-close-choice compose-close-choice-floating"
      panel.setAttribute("popover", "auto")
      var heading = document.createElement("h2")
      heading.textContent = title
      var body = document.createElement("p")
      body.textContent = text
      panel.appendChild(heading)
      panel.appendChild(body)
      var textField = null
      if (options && options.input) {
        textField = document.createElement("input")
        textField.type = "text"
        textField.value = options.input.value || ""
        textField.placeholder = options.input.placeholder || ""
        textField.setAttribute("aria-label", title)
        textField.className = "mt-3 h-9 w-full rounded-md border border-input bg-background px-3 text-sm text-foreground"
        textField.addEventListener("keydown", function (event) {
          if (event.key !== "Enter") return
          event.preventDefault()
          finish("ok")
        })
        panel.appendChild(textField)
      }
      var settled = false
      function finish(action) {
        if (settled) return
        settled = true
        if (textField) options.input.value = textField.value
        panel.removeEventListener("toggle", onToggle)
        if (panel.matches && panel.matches(":popover-open")) panel.hidePopover()
        panel.remove()
        resolve(action)
      }
      function onToggle(event) {
        if (event.newState === "closed") finish("cancel")
      }
      var row = document.createElement("div")
      row.className = "compose-close-choice-actions"
      actions.forEach(function (a) {
        var btn = document.createElement("button")
        btn.type = "button"
        btn.textContent = a.label
        btn.dataset.composeCloseAction = a.action
        if (a.primary) btn.className = "compose-close-choice-primary"
        row.appendChild(btn)
      })
      panel.appendChild(row)
      panel.addEventListener("click", function (event) {
        var btn = event.target && event.target.closest ? event.target.closest("[data-compose-close-action]") : null
        if (!btn) return
        finish(btn.dataset.composeCloseAction)
      })
      panel.addEventListener("toggle", onToggle)
      // A hover tooltip (e.g. the toolbar button that opened this) would sit over the heading.
      if (window.tui && window.tui.popover && window.tui.popover.closeAll) window.tui.popover.closeAll()
      var dialogs = document.querySelectorAll("dialog[open]")
      ;(dialogs.length ? dialogs[dialogs.length - 1] : document.body).appendChild(panel)
      if (panel.showPopover) panel.showPopover()
      if (textField) {
        textField.focus()
        textField.select()
      }
    })
  }

  // In-app replacement for window.prompt (which returns null silently in the desktop WKWebView).
  // Resolves the entered text, or null when cancelled.
  window.goferPrompt = function (title, text, opts) {
    opts = opts || {}
    var input = { value: opts.value || "", placeholder: opts.placeholder || "" }
    return window.goferChoice(title, text, [
      { label: "Cancel", action: "cancel" },
      { label: opts.confirmLabel || "OK", action: "ok", primary: true }
    ], { input: input }).then(function (action) { return action === "ok" ? input.value : null })
  }

  window.goferConfirm = function (title, text, confirmLabel) {
    return goferChoice(title, text, [
      { label: "Cancel", action: "cancel" },
      { label: confirmLabel, action: "ok", primary: true }
    ]).then(function (action) { return action === "ok" })
  }

  // <form data-confirm="Question?"> asks before submitting (plain or htmx forms).
  document.addEventListener("submit", function (e) {
    var form = e.target
    if (!form || !form.dataset || !form.dataset.confirm) return
    if (form.dataset.confirmed === "true") {
      delete form.dataset.confirmed
      return
    }
    e.preventDefault()
    e.stopImmediatePropagation()
    var submitter = e.submitter || null
    window.goferConfirm("Are you sure?", form.dataset.confirm, form.dataset.confirmLabel || "Continue").then(function (ok) {
      if (!ok) return
      form.dataset.confirmed = "true"
      if (form.requestSubmit) form.requestSubmit(submitter)
      else form.submit()
    })
  }, true)
})()
