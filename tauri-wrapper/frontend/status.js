// Renders the startup state pushed from src-tauri/src/lib.rs. Kept as a
// separate file (not inline, no onclick attributes) so the app's CSP can stay
// at script-src 'self'.
(function () {
  var title = document.getElementById("title");
  var detail = document.getElementById("detail");
  var tail = document.getElementById("tail");
  var retry = document.getElementById("retry");

  function render(view) {
    var error = view.state === "error";
    var slow = view.state === "slow";
    title.textContent = error
      ? "Raven couldn't start"
      : slow
        ? "Updating your mailbox… this can take a few minutes after an update"
        : "Starting Raven…";
    detail.textContent = view.detail || "";
    detail.hidden = !view.detail;
    tail.textContent = view.tail || "";
    tail.hidden = !error || !view.tail;
    retry.hidden = !error;
    retry.disabled = false;
  }

  var tauri = window.__TAURI__;
  var invoke = tauri && tauri.core && tauri.core.invoke;
  window.ravenStatus = { render: render };
  if (!invoke) return;

  retry.addEventListener("click", function () {
    retry.disabled = true;
    invoke("retry_startup").catch(function () {
      retry.disabled = false;
    });
  });
  invoke("startup_state").then(render).catch(function () {});
})();
