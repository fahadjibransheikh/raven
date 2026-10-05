package ical

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// A lossless component tree for editing calendar objects (CalDAV writes).
// Unlike Parse, which reads one invitation for display, the tree keeps every
// property line exactly as written (X- props, VALARMs, VTIMEZONEs, unknown
// parameters) so a read-modify-write does not drop what we do not model.

const (
	// MaxDocBytes bounds a calendar object we are willing to edit. It is an
	// error, never a truncation: writing back a cut-off object would lose data.
	MaxDocBytes = 4 << 20
	maxDocDepth = 8
)

// Prop is one content line. Head is NAME plus ";PARAM=..." exactly as written.
type Prop struct {
	Head  string
	Value string
}

// Name is the upper-case property name.
func (p Prop) Name() string {
	name, _, _ := strings.Cut(p.Head, ";")
	return strings.ToUpper(strings.TrimSpace(name))
}

// Param returns one parameter value (unquoted), "" when absent.
func (p Prop) Param(key string) string {
	parts := splitOutsideQuotes(p.Head, ';')
	for _, kv := range parts[1:] {
		k, v, ok := strings.Cut(kv, "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), key) {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return ""
}

// WithParam returns the property with a parameter set (replacing an existing one).
func (p Prop) WithParam(key, value string) Prop {
	parts := splitOutsideQuotes(p.Head, ';')
	out := []string{parts[0]}
	for _, kv := range parts[1:] {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(strings.TrimSpace(k), key) {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, key+"="+value)
	return Prop{Head: strings.Join(out, ";"), Value: p.Value}
}

// Comp is a BEGIN:NAME ... END:NAME block.
type Comp struct {
	Name     string
	Props    []Prop
	Children []*Comp
}

// ParseDoc parses a calendar object and returns its VCALENDAR.
func ParseDoc(data []byte) (*Comp, error) {
	if len(data) > MaxDocBytes {
		return nil, errors.New("ical: calendar object too large")
	}
	var root *Comp
	var stack []*Comp
	for _, ln := range unfoldAll(string(data)) {
		head, value, ok := splitLine(ln)
		if !ok {
			continue
		}
		p := Prop{Head: head, Value: value}
		switch p.Name() {
		case "BEGIN":
			c := &Comp{Name: strings.ToUpper(strings.TrimSpace(value))}
			if len(stack) == 0 {
				if c.Name != "VCALENDAR" {
					return nil, errors.New("ical: expected BEGIN:VCALENDAR")
				}
				root = c
			} else {
				if len(stack) >= maxDocDepth {
					return nil, errors.New("ical: components nested too deeply")
				}
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, c)
			}
			stack = append(stack, c)
		case "END":
			if len(stack) == 0 || stack[len(stack)-1].Name != strings.ToUpper(strings.TrimSpace(value)) {
				return nil, errors.New("ical: mismatched END")
			}
			stack = stack[:len(stack)-1]
		default:
			if len(stack) == 0 {
				continue
			}
			c := stack[len(stack)-1]
			c.Props = append(c.Props, p)
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, errors.New("ical: unterminated VCALENDAR")
	}
	return root, nil
}

func unfoldAll(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var out []string
	for _, raw := range strings.Split(s, "\n") {
		if (strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t")) && len(out) > 0 {
			out[len(out)-1] += raw[1:]
			continue
		}
		out = append(out, raw)
	}
	return out
}

// splitLine splits at the first colon outside quotes.
func splitLine(ln string) (head, value string, ok bool) {
	inQ := false
	for i := 0; i < len(ln); i++ {
		switch ln[i] {
		case '"':
			inQ = !inQ
		case ':':
			if !inQ {
				return ln[:i], ln[i+1:], i > 0
			}
		}
	}
	return "", "", false
}

// Get returns the first property with the name, or nil.
func (c *Comp) Get(name string) *Prop {
	for i := range c.Props {
		if c.Props[i].Name() == name {
			return &c.Props[i]
		}
	}
	return nil
}

// All returns every property with the name.
func (c *Comp) All(name string) []Prop {
	var out []Prop
	for _, p := range c.Props {
		if p.Name() == name {
			out = append(out, p)
		}
	}
	return out
}

// Remove drops every property with the name.
func (c *Comp) Remove(name string) {
	kept := c.Props[:0]
	for _, p := range c.Props {
		if p.Name() != name {
			kept = append(kept, p)
		}
	}
	c.Props = kept
}

// Set replaces the property (all copies) with one line; it is appended if absent.
func (c *Comp) Set(head, value string) {
	name, _, _ := strings.Cut(head, ";")
	name = strings.ToUpper(name)
	for i := range c.Props {
		if c.Props[i].Name() == name {
			c.Props[i] = Prop{Head: head, Value: value}
			kept := c.Props[:i+1]
			for _, p := range c.Props[i+1:] {
				if p.Name() != name {
					kept = append(kept, p)
				}
			}
			c.Props = kept
			return
		}
	}
	c.Props = append(c.Props, Prop{Head: head, Value: value})
}

// Add appends a property line.
func (c *Comp) Add(head, value string) { c.Props = append(c.Props, Prop{Head: head, Value: value}) }

// Clone deep-copies the component.
func (c *Comp) Clone() *Comp {
	n := &Comp{Name: c.Name, Props: append([]Prop(nil), c.Props...)}
	for _, ch := range c.Children {
		n.Children = append(n.Children, ch.Clone())
	}
	return n
}

// Bytes serialises with CRLF line ends and RFC 5545 section 3.1 folding.
func (c *Comp) Bytes() []byte {
	var b strings.Builder
	c.write(&b)
	return []byte(b.String())
}

func (c *Comp) write(b *strings.Builder) {
	b.WriteString("BEGIN:" + c.Name + "\r\n")
	for _, p := range c.Props {
		foldLine(b, p.Head+":"+strings.NewReplacer("\r", "", "\n", "").Replace(p.Value))
	}
	for _, ch := range c.Children {
		ch.write(b)
	}
	b.WriteString("END:" + c.Name + "\r\n")
}

// foldLine wraps at 75 octets (continuations carry a leading space, so 74 of
// content), never splitting a UTF-8 sequence.
func foldLine(b *strings.Builder, line string) {
	limit := 75
	for len(line) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		b.WriteString(line[:cut])
		b.WriteString("\r\n ")
		line = line[cut:]
		limit = 74
	}
	b.WriteString(line)
	b.WriteString("\r\n")
}

// EscapeText applies RFC 5545 section 3.3.11 TEXT escaping.
func EscapeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`).Replace(s)
}

// UnescapeText reverses EscapeText.
func UnescapeText(s string) string { return unescapeText(s) }

// NewCalendar returns an empty VCALENDAR with the mandatory header properties.
func NewCalendar() *Comp {
	c := &Comp{Name: "VCALENDAR"}
	c.Add("VERSION", "2.0")
	c.Add("PRODID", "-//Raven//Calendar//EN")
	c.Add("CALSCALE", "GREGORIAN")
	return c
}

// UTCStamp formats an instant as a UTC DATE-TIME ("20060102T150405Z").
func UTCStamp(t time.Time) string { return t.UTC().Format("20060102T150405Z") }

// TimeProp renders a DATE / UTC / TZID date-time property. tz is an IANA name
// ("" means UTC); it is ignored for all-day values (floating dates).
func TimeProp(name string, t time.Time, allDay bool, tz string) (head, value string) {
	switch {
	case allDay:
		return name + ";VALUE=DATE", t.Format("20060102")
	case tz != "" && tz != "UTC":
		if loc, err := time.LoadLocation(tz); err == nil {
			return name + ";TZID=" + tz, t.In(loc).Format("20060102T150405")
		}
	}
	return name, UTCStamp(t)
}
