package models

import "testing"

func TestCleanDisplayName(t *testing.T) {
	for in, want := range map[string]string{
		`"Expedia.com"`:    "Expedia.com",
		`  "Expedia.com" `: "Expedia.com",
		`""Nested""`:       "Nested",
		`Joe "JD" Doe`:     `Joe "JD" Doe`,
		`"Unbalanced`:      `"Unbalanced`,
		`"`:                `"`,
		``:                 "",
		`Plain Name`:       "Plain Name",
	} {
		if got := CleanDisplayName(in); got != want {
			t.Errorf("CleanDisplayName(%q) = %q, want %q", in, got, want)
		}
	}
}
