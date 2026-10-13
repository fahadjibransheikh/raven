// Phone calendar: the agenda is a second screen. The "Agenda" button in the header
// opens it on html[data-calendar-phone-agenda] (shown by the Calendar block in input.css);
// its back button closes it and, if a day is selected, clears that selection so the
// month grid comes back (app.js owns day selection and listens for the clear button).
(function () {
  var root = document.documentElement
  document.addEventListener("click", function (e) {
    var t = e.target
    if (!t || !t.closest) return
    if (t.closest("[data-calendar-agenda-toggle]")) {
      root.setAttribute("data-calendar-phone-agenda", "")
    } else if (t.closest("[data-calendar-agenda-back]")) {
      root.removeAttribute("data-calendar-phone-agenda")
      var clear = document.querySelector("[data-calendar-agenda-clear]")
      if (clear && !clear.hidden) clear.click()
    }
  })
})()
