// What's New window: shown once after an update, and on demand from the help menu and command palette.
(function () {
  "use strict";
  if (window.RavenWhatsNew) return;

  var ID = "whats-new-dialog";

  // GoferSettings keeps a cache that later settings saves send back in full,
  // so it has to learn the value too or a later save would erase it.
  function remember(version) {
    if (!version) return;
    if (window.GoferSettings && window.GoferSettings.set) window.GoferSettings.set("whats_new_seen", version);
    fetch("/api/settings/ui", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ whats_new_seen: version }),
    }).catch(function () {});
  }

  function show(data) {
    var old = document.getElementById(ID);
    if (old) old.remove();
    var holder = document.createElement("div");
    holder.innerHTML = data.html;
    var root = holder.firstElementChild;
    document.body.appendChild(root);
    var dlg = root.querySelector("dialog");
    // "close" fires for every way out: Got it, X, Esc, click-away.
    dlg.addEventListener("close", function () {
      remember(data.version);
      root.remove();
    });
    window.tui.dialog.open(ID);
  }

  function load(mode) {
    return fetch("/api/whats-new" + (mode === "open" ? "?mode=open" : ""), { headers: { Accept: "application/json" } })
      .then(function (r) { return r.ok ? r.json() : null })
      .then(function (data) {
        if (!data) return;
        if (data.action === "show" && data.html && window.tui && window.tui.dialog) show(data);
        else if (data.action === "record") remember(data.version);
      })
      .catch(function () {});
  }

  window.RavenWhatsNew = {
    open: function () { return load("open") },
  };

  document.addEventListener("click", function (e) {
    if (e.target.closest && e.target.closest("[data-whats-new-open]")) {
      e.preventDefault();
      window.RavenWhatsNew.open();
    }
  });

  function auto() {
    // Not while another dialog (compose, add account) is open.
    if (document.querySelector("dialog[open]")) return;
    load("auto");
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", function () { setTimeout(auto, 600) });
  else setTimeout(auto, 600);
})();
