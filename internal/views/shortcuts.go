package views

// shortcutKeys maps registry ids (MAIL_SHORTCUTS in assets/js/app.js) to the key label
// shown on toolbar chips and tooltips. tests/js/shortcut_chips.test.js fails if a label
// here drifts from the registry's first key.
var shortcutKeys = map[string]string{
	"archive":   "E",
	"delete":    "#",
	"star":      "S",
	"move":      "V",
	"read":      "U",
	"reply":     "R",
	"reply-all": "A",
	"forward":   "F",
}

// withShortcut appends the key to a tooltip or aria label: "Delete (#)".
func withShortcut(label, id string) string {
	return label + " (" + shortcutKeys[id] + ")"
}
