// One-time move of browser storage entries written before the rename
// ("gofer:ui_settings", "gofer_mailto_handler_state", ...) to their new
// "raven:" / "raven_" names. It runs ahead of every script that reads them, a
// value already stored under the new name wins, and once the old entries are
// gone it does nothing.
(function () {
  ["localStorage", "sessionStorage"].forEach(function (name) {
    try {
      var store = window[name];
      var legacyKeys = [];
      for (var i = 0; i < store.length; i++) {
        var key = store.key(i);
        if (key && /^gofer[:_]/.test(key)) legacyKeys.push(key);
      }
      legacyKeys.forEach(function (key) {
        var next = "raven" + key.slice(5);
        if (store.getItem(next) === null) store.setItem(next, store.getItem(key));
        store.removeItem(key);
      });
    } catch (_) {
      // Storage can be unavailable (private mode, blocked); nothing to migrate then.
    }
  });
})();
