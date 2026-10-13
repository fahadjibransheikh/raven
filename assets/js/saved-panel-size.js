// Applies the saved panel sizes before the page is drawn, so it doesn't jump.
(function() {
  document.documentElement.classList.add("js");
  // Set before first paint so phones never flash the desktop panes; phone-layout.js keeps it in sync.
  if (window.matchMedia && window.matchMedia("(max-width: 1023.98px)").matches) document.documentElement.setAttribute("data-phone", "");

  var settings = {};
  try {
    settings = JSON.parse(localStorage.getItem("gofer:ui_settings") || "{}") || {};
  } catch (_) {}

  function px(value, min, max) {
    var n = parseInt(value, 10);
    if (isNaN(n) || n <= 0) return null;
    if (max === null || max === undefined) return Math.max(min, n);
    return Math.max(min, Math.min(max, n));
  }

  function mailListWidth(value) {
    var raw = String(value || "").trim();
    if (raw.slice(-1) === "%") {
      var percent = parseFloat(raw);
      if (!isNaN(percent) && percent > 0) return "clamp(300px," + percent + "%,calc(100% - 300px))";
    }
    var n = px(raw, 300);
    return n ? n + "px" : null;
  }

  var vw = window.innerWidth || 1024;
  var stacked = settings.mail_pane_layout === "stacked";
  var sidebar = px(settings.sidebar_width, 180, Math.min(400, vw * 0.25));
  var mailList = stacked ? px(settings.mail_list_height, 180) : mailListWidth(settings.mail_list_width);
  if (!sidebar && !mailList && !stacked) return;

  var style = document.createElement("style");
  style.setAttribute("data-saved-panel-size-style", "");
  style.textContent = (sidebar ? "aside{width:" + sidebar + "px!important}" : "") + (stacked ? "#main-content{flex-direction:column}#mail-list{width:100%!important" + (mailList ? ";height:" + mailList + "px!important" : "") + "}" : (mailList ? "#mail-list{width:" + mailList + "!important}" : ""));
  document.head.appendChild(style);
})();
