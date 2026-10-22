// Native window chrome for the Raven page. lib.rs prepends
// `window.__RAVEN_DESKTOP_OS__ = "<std::env::consts::OS>";`.
//
// - Every platform: the native title bar follows the page's light/dark theme
//   (the window config starts dark), so a light Raven no longer sits under a
//   black bar. In System mode the window follows the OS instead: setTheme pins
//   the app's appearance, and the page's prefers-color-scheme reports the
//   pinned value, so pinning would stop System mode from ever changing.
// - macOS: the title bar is an overlay (tauri.conf.json titleBarStyle), so the
//   page draws a strip the traffic lights sit in; input.css styles it from
//   html[data-desktop="macos"] and offsets the app shell below it.
//
// Same origin check as unread_badge.js: this also runs on the placeholder page
// and on OAuth provider pages.
(function () {
  if (window.location.protocol !== "http:" || window.location.hostname !== "127.0.0.1") {
    return;
  }
  var os = window.__RAVEN_DESKTOP_OS__ || "";
  var root = document.documentElement;
  root.setAttribute("data-desktop", os);

  var lastTheme = null;
  function syncTheme() {
    var system = root.getAttribute("data-theme-mode") === "system";
    var theme = system ? "system" : root.classList.contains("dark") ? "dark" : "light";
    var tauriWindow = window.__TAURI__ && window.__TAURI__.window;
    if (theme === lastTheme || !tauriWindow) return;
    lastTheme = theme;
    // null follows the OS.
    tauriWindow.getCurrentWindow().setTheme(system ? null : theme).catch(function (err) {
      lastTheme = null;
      console.warn("window theme:", err);
    });
  }
  new MutationObserver(syncTheme).observe(root, { attributes: true, attributeFilter: ["class", "data-theme-mode"] });

  function start() {
    syncTheme();
    if (os !== "macos" || document.querySelector("[data-desktop-titlebar]")) return;
    var bar = document.createElement("div");
    bar.setAttribute("data-desktop-titlebar", "");
    // Tauri's drag script: drag the window from the bar, double-click zooms it.
    bar.setAttribute("data-tauri-drag-region", "");
    document.body.prepend(bar);
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start);
  } else {
    start();
  }
})();
