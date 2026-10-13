// Package legacyenv reads Raven's RAVEN_* environment variables and falls back
// to the GOFER_* names that installs made before the rename still have in their
// .env files. Every read of a RAVEN_* variable goes through here.
package legacyenv

import (
	"log"
	"os"
	"sort"
	"strings"
)

const (
	currentPrefix = "RAVEN_"
	legacyPrefix  = "GOFER_"
)

func legacyName(name string) string {
	if !strings.HasPrefix(name, currentPrefix) {
		return ""
	}
	return legacyPrefix + strings.TrimPrefix(name, currentPrefix)
}

// Lookup returns the value of name (a RAVEN_* variable), or of its GOFER_*
// predecessor when name itself is not set.
func Lookup(name string) (string, bool) {
	if value, ok := os.LookupEnv(name); ok {
		return value, true
	}
	if legacy := legacyName(name); legacy != "" {
		return os.LookupEnv(legacy)
	}
	return "", false
}

// Get is Lookup without the found flag, like os.Getenv.
func Get(name string) string {
	value, _ := Lookup(name)
	return value
}

// LogLegacy logs, once, which GOFER_* variables are set so the owner can rename
// them. It never prints values (several are secrets).
func LogLegacy() {
	var used, shadowed []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, legacyPrefix) {
			continue
		}
		current := currentPrefix + strings.TrimPrefix(key, legacyPrefix)
		if _, ok := os.LookupEnv(current); ok {
			shadowed = append(shadowed, key)
		} else {
			used = append(used, key)
		}
	}
	sort.Strings(used)
	sort.Strings(shadowed)
	if len(used) > 0 {
		log.Printf("config: using legacy environment names %s; rename them to %s to silence this", strings.Join(used, ", "), currentPrefix+"*")
	}
	if len(shadowed) > 0 {
		log.Printf("config: ignoring legacy environment names %s because the %s* name is also set", strings.Join(shadowed, ", "), currentPrefix)
	}
}
