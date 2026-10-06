// Package whatsnew parses the embedded release notes (releases.md) and decides
// whether a user should be shown the "What's New" window.
package whatsnew

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

//go:embed releases.md
var source string

// BuildVersion is set at build time by the desktop release workflow
// (-ldflags "-X .../internal/whatsnew.BuildVersion=0.1.13"). Builds without it
// (web server built with plain `go build`, `go run`) use the newest notes entry.
var BuildVersion string

// NextVersion is the placeholder version for the unreleased build.
const NextVersion = "next"

// MaxShown caps how many releases one window shows.
const MaxShown = 3

type Group struct {
	Name  string
	Items []string
}

type Release struct {
	Version string
	Date    string
	Title   string
	Hero    string
	Groups  []Group
}

// Parse reads the releases.md format: "## version | date | title" headings,
// an optional hero paragraph, then "### Group" headings with "- " bullets.
func Parse(src string) ([]Release, error) {
	var out []Release
	var cur *Release
	var group *Group
	inComment := false
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
		}
	}
	for n, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if inComment {
			inComment = !strings.Contains(line, "-->")
			continue
		}
		if strings.HasPrefix(line, "<!--") {
			inComment = !strings.Contains(line, "-->")
			continue
		}
		switch {
		case strings.HasPrefix(line, "## "):
			flush()
			parts := strings.Split(strings.TrimPrefix(line, "## "), "|")
			if len(parts) != 3 {
				return nil, fmt.Errorf("line %d: want \"## version | date | title\", got %q", n+1, line)
			}
			cur = &Release{Version: strings.TrimSpace(parts[0]), Date: strings.TrimSpace(parts[1]), Title: strings.TrimSpace(parts[2])}
			if cur.Version == "" || cur.Title == "" {
				return nil, fmt.Errorf("line %d: version and title are required", n+1)
			}
			group = nil
		case cur == nil || line == "":
		case strings.HasPrefix(line, "### "):
			cur.Groups = append(cur.Groups, Group{Name: strings.TrimSpace(strings.TrimPrefix(line, "### "))})
			group = &cur.Groups[len(cur.Groups)-1]
		case strings.HasPrefix(line, "- "):
			if group == nil {
				return nil, fmt.Errorf("line %d: bullet before any ### group", n+1)
			}
			group.Items = append(group.Items, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
		case group == nil && cur.Hero == "":
			cur.Hero = line
		default:
			return nil, fmt.Errorf("line %d: unexpected text %q", n+1, line)
		}
	}
	flush()
	return out, nil
}

var (
	once     sync.Once
	releases []Release
	parseErr error
)

// Releases returns the embedded notes, newest first.
func Releases() ([]Release, error) {
	once.Do(func() { releases, parseErr = Parse(source) })
	return releases, parseErr
}

// Current is the running build's version: BuildVersion when set, otherwise the
// newest notes entry (which is "next" before a release is cut).
func Current(rels []Release) string {
	if v := strings.TrimPrefix(strings.TrimSpace(BuildVersion), "v"); v != "" {
		return v
	}
	if len(rels) > 0 {
		return rels[0].Version
	}
	return ""
}

// Compare orders dotted versions numerically. "next" is newer than any number;
// non-numeric parts and pre-release suffixes count as 0 / are ignored.
func Compare(a, b string) int {
	a, b = norm(a), norm(b)
	if a == NextVersion || b == NextVersion {
		switch {
		case a == b:
			return 0
		case a == NextVersion:
			return 1
		}
		return -1
	}
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		x, y := part(pa, i), part(pb, i)
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func norm(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	return v
}

func part(p []string, i int) int {
	if i >= len(p) {
		return 0
	}
	n, _ := strconv.Atoi(p[i])
	return n
}

type Action int

const (
	None   Action = iota // nothing to do
	Record               // store the current version silently
	Show                 // show the window, then store the version when dismissed
)

// Decide says what to do for a user. lastSeen is their stored version ("" if
// none), hasAccounts whether they have connected any account. A user with no
// stored version and no accounts is a brand-new install: record silently.
// A user with accounts and no stored version is an existing user who just
// updated to the first release with this feature: show them the newest notes.
// Returned releases are newest first, "next" resolved to current, capped at max.
func Decide(lastSeen, current string, hasAccounts bool, rels []Release, max int) (Action, []Release) {
	if current == "" {
		return None, nil
	}
	lastSeen = strings.TrimSpace(lastSeen)
	if lastSeen == NextVersion && current != NextVersion {
		lastSeen = "0" // seen only a dev build; a real release is still news
	}
	if lastSeen == "" && !hasAccounts {
		return Record, nil
	}
	if lastSeen != "" && Compare(current, lastSeen) <= 0 {
		return None, nil
	}
	var unseen []Release
	for _, r := range rels {
		if r.Version == NextVersion {
			r.Version = current
		}
		if Compare(r.Version, current) > 0 {
			continue
		}
		if lastSeen != "" && Compare(r.Version, lastSeen) <= 0 {
			continue
		}
		unseen = append(unseen, r)
		if len(unseen) == max {
			break
		}
	}
	if len(unseen) == 0 {
		return Record, nil
	}
	if lastSeen == "" {
		unseen = unseen[:1]
	}
	return Show, unseen
}

// Latest returns the newest releases (next resolved to current), for reopening
// the window on demand.
func Latest(current string, rels []Release, max int) []Release {
	var out []Release
	for _, r := range rels {
		if r.Version == NextVersion && current != "" {
			r.Version = current
		}
		out = append(out, r)
		if len(out) == max {
			break
		}
	}
	return out
}
