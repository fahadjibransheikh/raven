package views

import "encoding/json"

// onAction is the value of a data-on-* attribute: the function
// assets/js/actions.js calls when the event happens, and its arguments.
// Raven's pages run no inline handlers, so this replaces onclick="…".
func onAction(name string, args ...any) string {
	value, err := json.Marshal(append([]any{name}, args...))
	if err != nil {
		return "[]"
	}
	return string(value)
}

// Arguments that stand for something only the browser knows.
const (
	actionElement = "$this"
	actionEvent   = "$event"
	actionChecked = "$checked"
)

// actionClosest stands for the nearest ancestor of the element matching
// selector.
func actionClosest(selector string) string { return "$closest:" + selector }

// actionData stands for one of the element's data-* values, by its dataset
// name.
func actionData(key string) string { return "$data:" + key }
