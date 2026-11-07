package mapping

import (
	"fmt"
	"strings"
)

// Tag is a parsed struct tag: `tenet:"column:email,unique,length:180"`.
// Options are comma separated; a value follows the first colon. Values may
// be single-quoted to contain commas: default:'a,b'.
type Tag struct {
	Options map[string]string
	order   []string
}

// ParseTag parses the tag value.
func ParseTag(s string) (Tag, error) {
	t := Tag{Options: map[string]string{}}
	var parts []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && inQuote && i+1 < len(s) && s[i+1] == '\'':
			cur.WriteString("''")
			i++
		case c == '\'':
			inQuote = !inQuote
			cur.WriteByte(c)
		case c == ',' && !inQuote:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if inQuote {
		return t, fmt.Errorf("unterminated quote in tag %q", s)
	}
	parts = append(parts, cur.String())
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		key, val, _ := strings.Cut(p, ":")
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if _, dup := t.Options[key]; dup {
			return t, fmt.Errorf("duplicate tag option %q", key)
		}
		t.Options[key] = val
		t.order = append(t.order, key)
	}
	return t, nil
}

// Has reports whether the option is present.
func (t Tag) Has(key string) bool { _, ok := t.Options[key]; return ok }

// Get returns the option value.
func (t Tag) Get(key string) string { return t.Options[key] }

// Keys returns the options in declaration order.
func (t Tag) Keys() []string { return t.order }

// Unquote removes surrounding single quotes and un-doubles inner quotes.
// Defaults keep their quotes because they are SQL literals.
func Unquote(v string) string {
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	}
	return v
}

// List splits a "a|b|c" option value.
func List(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
