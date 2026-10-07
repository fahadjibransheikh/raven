// Reflects Gofer's unified unread count in document.title as a "(N) " prefix,
// so the Rust side (on_document_title_changed in lib.rs) can parse it back out
// and set the Dock/taskbar badge. Only ever runs against the real Gofer
// origin -- this init script also runs on the placeholder frontend/index.html
// page and on OAuth provider pages, so the origin check is not optional. The
// port varies per launch (lib.rs pick_port), so it is not part of the check;
// only the wrapper ever points this window at 127.0.0.1.
//
// The page already refetches /api/folders/unread whenever the server pushes a
// new-mail or mutation event over SSE, so instead of polling every 30s we
// listen to those responses. A slow poll remains as a fallback for a dropped
// SSE connection.
(function () {
  if (window.location.protocol !== "http:" || window.location.hostname !== "127.0.0.1") {
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

  var UNREAD_PATH = "/api/folders/unread";
  var FALLBACK_POLL_MS = 5 * 60 * 1000;
  var nativeFetch = window.fetch.bind(window);

  // Pick up the counts the page fetches in response to SSE events.
  window.fetch = function (input) {
    var promise = nativeFetch.apply(window, arguments);
    try {
      var url = typeof input === "string" ? input : (input && input.url) || "";
      if (url.indexOf(UNREAD_PATH) !== -1) {
        promise
          .then(function (res) {
            return res.ok ? res.clone().json() : null;
          })
          .then(function (counts) {
            if (counts) {
              applyCount(counts.inbox || 0);
            }
          })
          .catch(function () {});
      }
    } catch (_) {
      // Never let badge bookkeeping break the page's own request.
    }
    return promise;
  };

  function poll() {
    nativeFetch(UNREAD_PATH)
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

  setInterval(poll, FALLBACK_POLL_MS);
})();
