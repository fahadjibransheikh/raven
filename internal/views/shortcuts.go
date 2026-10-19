package views

// shortcutKeys maps registry ids (MAIL_SHORTCUTS in assets/js/app.js) to the key label
// shown on toolbar chips and tooltips. tests/js/shortcut_chips.test.js fails if a label
// here drifts from the registry's first key.
var shortcutKeys = map[string]string{
	"archive":   "E",
	"delete":    "#",
	"star":      "S",
	"move":      "V",
	"snooze":    "B",
	"label":     "L",
	"read":      "U",
	"reply":     "R",
	"reply-all": "A",
	"forward":   "F",
}

// withShortcut appends the key to a tooltip or aria label: "Delete (#)".
func withShortcut(label, id string) string {
	return label + " (" + shortcutKeys[id] + ")"
}

type timerPreset struct{ Label, Value string }

// Values are the presets snoozeThread and followUpThread understand in app.js.
var snoozePresets = []timerPreset{
	{"Later today", "later"}, {"Tomorrow", "tomorrow"}, {"This weekend", "weekend"},
	{"Next week", "next-week"}, {"Pick a date and time\u2026", "custom"},
}

var followUpPresets = []timerPreset{
	{"In 1 day", "1"}, {"In 3 days", "3"}, {"In a week", "7"}, {"Pick a date and time\u2026", "custom"},
}
