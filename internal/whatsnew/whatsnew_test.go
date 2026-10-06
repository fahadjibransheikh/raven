package whatsnew

import "testing"

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"0.1.13", "0.1.12", 1}, {"0.1.9", "0.1.10", -1}, {"v1.0.0", "1.0", 0},
		{"0.2.0", "0.1.99", 1}, {"1.0.0-beta.1", "1.0.0", 0},
		{"next", "99.0.0", 1}, {"0.1.0", "next", -1}, {"next", "next", 0},
	} {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

const sample = `<!-- comment
spanning lines -->

## 0.2.0 | 2026-02-01 | Second
Hero two.

### New
- A
- B

### Fixed
- C

## 0.1.0 | 2026-01-01 | First

### New
- D
`

func TestParse(t *testing.T) {
	r, err := Parse(sample)
	if err != nil || len(r) != 2 {
		t.Fatalf("got %v, %v", r, err)
	}
	if r[0].Version != "0.2.0" || r[0].Date != "2026-02-01" || r[0].Title != "Second" || r[0].Hero != "Hero two." {
		t.Errorf("header: %+v", r[0])
	}
	if len(r[0].Groups) != 2 || r[0].Groups[0].Name != "New" || len(r[0].Groups[0].Items) != 2 || r[0].Groups[1].Items[0] != "C" {
		t.Errorf("groups: %+v", r[0].Groups)
	}
	if r[1].Hero != "" || len(r[1].Groups) != 1 {
		t.Errorf("second: %+v", r[1])
	}
	for _, bad := range []string{"## 1.0 | only two", "## 1.0 | d | t\n- orphan bullet", "## 1.0 | d | t\n### New\nstray prose"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestEmbeddedNotesParse(t *testing.T) {
	r, err := Releases()
	if err != nil || len(r) == 0 {
		t.Fatalf("embedded notes: %v %v", r, err)
	}
	items := 0
	for _, g := range r[0].Groups {
		items += len(g.Items)
	}
	if items < 8 || items > 14 {
		t.Errorf("newest entry has %d bullets, want a short list", items)
	}
}

func TestDecide(t *testing.T) {
	rels, _ := Parse(sample)
	v := func(rs []Release) []string {
		var o []string
		for _, r := range rs {
			o = append(o, r.Version)
		}
		return o
	}
	for _, c := range []struct {
		name, last, cur string
		accounts        bool
		want            Action
		versions        []string
	}{
		{"first run, no accounts: record silently", "", "0.2.0", false, Record, nil},
		{"existing user, nothing stored: newest only", "", "0.2.0", true, Show, []string{"0.2.0"}},
		{"upgrade shows unseen newest first", "0.0.9", "0.2.0", true, Show, []string{"0.2.0", "0.1.0"}},
		{"one version behind", "0.1.0", "0.2.0", true, Show, []string{"0.2.0"}},
		{"already seen", "0.2.0", "0.2.0", true, None, nil},
		{"downgrade or older build", "0.3.0", "0.2.0", true, None, nil},
		{"newer build without notes: record", "0.2.0", "0.2.1", true, Record, nil},
		{"no stored version but no-accounts user later: still silent", "", "0.2.0", false, Record, nil},
		{"unknown current", "0.1.0", "", true, None, nil},
	} {
		got, rs := Decide(c.last, c.cur, c.accounts, rels, MaxShown)
		if got != c.want || (c.versions != nil && len(v(rs)) != len(c.versions)) {
			t.Errorf("%s: action=%v versions=%v", c.name, got, v(rs))
			continue
		}
		for i := range c.versions {
			if v(rs)[i] != c.versions[i] {
				t.Errorf("%s: versions=%v want %v", c.name, v(rs), c.versions)
			}
		}
	}
	if _, rs := Decide("0.0.1", "0.2.0", true, rels, 1); len(rs) != 1 {
		t.Errorf("cap not applied: %v", v(rs))
	}
}

func TestNextIsTheCurrentBuild(t *testing.T) {
	rels, _ := Parse("## next | 2026-10-06 | T\n### New\n- X\n")
	if cur := Current(rels); cur != "next" {
		t.Fatalf("web build without BuildVersion: current=%q", cur)
	}
	if a, rs := Decide("0.1.12", "next", true, rels, MaxShown); a != Show || len(rs) != 1 {
		t.Errorf("dev build should show: %v %v", a, rs)
	}
	if a, _ := Decide("next", "next", true, rels, MaxShown); a != None {
		t.Errorf("seen next should not repeat: %v", a)
	}
	BuildVersion = "v0.1.13"
	defer func() { BuildVersion = "" }()
	a, rs := Decide("0.1.12", Current(rels), true, rels, MaxShown)
	if a != Show || len(rs) != 1 || rs[0].Version != "0.1.13" {
		t.Errorf("desktop build resolves next: %v %v", a, rs)
	}
	if a, _ := Decide("next", "0.1.13", true, rels, MaxShown); a != Show {
		t.Errorf("a stored dev 'next' must not hide a real release: %v", a)
	}
}
