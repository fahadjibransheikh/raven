// Phone layout state: sets html[data-phone] below the lg breakpoint.
// All phone styling lives in the @media block at the end of input.css.
(function () {
  if (!window.matchMedia) return
  var mq = window.matchMedia("(max-width: 1023.98px)")
  var root = document.documentElement

  function sync() {
    if (mq.matches) root.setAttribute("data-phone", "")
    else root.removeAttribute("data-phone")
  }

  sync()
  if (mq.addEventListener) mq.addEventListener("change", sync)
  else if (mq.addListener) mq.addListener(sync)
})()
