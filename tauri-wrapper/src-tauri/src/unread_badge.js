// Polls Gofer's unified unread count and reflects it in document.title as a
// "(N) " prefix, so the Rust side (on_document_title_changed in lib.rs) can
// parse it back out and set the Dock/taskbar badge. Only ever runs against
// the real Gofer origin -- this init script also runs on the placeholder
// frontend/index.html page (file://) before navigate() fires, so the origin
// check is not optional.
(function () {
  if (window.location.origin !== "http://127.0.0.1:8090") {
    return;
  }

  var PREFIX_RE = /^\(\d+\)\s/;
  var lastCount = 0;

  function currentBase() {
    return document.title.replace(PREFIX_RE, "");
  }

  function applyCount(n) {
    lastCount = n;
    var base = currentBase();
    var next = n > 0 ? "(" + n + ") " + base : base;
    if (document.title === next) {
      return;
    }
    document.title = next;
  }

  function poll() {
    fetch("/api/folders/unread")
      .then(function (res) {
        return res.ok ? res.json() : {};
      })
      .then(function (counts) {
        applyCount((counts && counts.inbox) || 0);
      })
      .catch(function () {
        // Network hiccup / server restarting -- leave the title as-is.
      });
  }

  // Init scripts run before <head> is parsed, so attach once the DOM exists.
  // Watching <head> also catches the <title> element itself being replaced.
  function observeTitle() {
    // Gofer's own page changes document.title (e.g. "Contacts — Raven").
    // Re-apply the last known count on top of that. Our own writes also land
    // here, but applyCount is a no-op when the title already matches.
    new MutationObserver(function () {
      applyCount(lastCount);
    }).observe(document.head, { childList: true, characterData: true, subtree: true });
  }
  function start() {
    observeTitle();
    poll();
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start);
  } else {
    start();
  }

  document.addEventListener("visibilitychange", function () {
    if (document.visibilityState === "visible") {
      poll();
    }
  });

  setInterval(poll, 30000);
})();
