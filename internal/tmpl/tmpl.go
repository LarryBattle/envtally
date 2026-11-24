// Package tmpl expands ${name} references in strings.
package tmpl

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var refPattern = regexp.MustCompile(`\$\{([^{}]*)\}`)

// Lookup returns the value for a reference name, and whether it exists.
type Lookup func(name string) (string, bool)

// Refs returns the names referenced in s, in order of appearance.
// It fails on an empty reference (${}) or an unterminated "${".
func Refs(s string) ([]string, error) {
	var names []string
	for _, m := range refPattern.FindAllStringSubmatch(s, -1) {
		name := strings.TrimSpace(m[1])
		if name == "" {
			return nil, errors.New("empty ${} reference")
		}
		names = append(names, name)
	}
	if strings.Contains(refPattern.ReplaceAllString(s, ""), "${") {
		return nil, fmt.Errorf("unterminated ${ in %q", s)
	}
	return names, nil
}

// Expand replaces every ${name} in s using lookup.
// Any name lookup does not know is an error.
func Expand(s string, lookup Lookup) (string, error) {
	if _, err := Refs(s); err != nil {
		return "", err
	}
	var missing []string
	out := refPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := strings.TrimSpace(match[2 : len(match)-1])
		value, ok := lookup(name)
		if !ok {
			missing = append(missing, "${"+name+"}")
			return match
		}
		return value
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("undefined variable %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// ExpandLoose replaces every ${name} in s using lookup. A name lookup does
// not know is replaced by fallback. It reports whether every name was found.
func ExpandLoose(s string, lookup Lookup, fallback string) (string, bool) {
	complete := true
	out := refPattern.ReplaceAllStringFunc(s, func(match string) string {
		value, ok := lookup(strings.TrimSpace(match[2 : len(match)-1]))
		if !ok {
			complete = false
			return fallback
		}
		return value
	})
	return out, complete
}

// Maps returns a Lookup that searches the maps in order; the first hit wins.
func Maps(maps ...map[string]string) Lookup {
	return func(name string) (string, bool) {
		for _, m := range maps {
			if v, ok := m[name]; ok {
				return v, true
			}
		}
		return "", false
	}
}
