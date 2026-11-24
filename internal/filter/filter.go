// Package filter selects instances with include/exclude regular expressions.
package filter

import (
	"fmt"
	"regexp"
	"strings"
)

// Fields are the values of one instance that a pattern can match.
type Fields struct {
	Project string
	App     string
	Env     string
	Host    string // host[:port]
	URL     string // full base URL
}

// Field names accepted in FIELD=REGEX, in the order they are documented.
const (
	fieldProject = "project"
	fieldApp     = "app"
	fieldEnv     = "env"
	fieldHost    = "host"
	fieldURL     = "url"
	fieldAny     = "" // a plain REGEX: any of the above
)

// FieldNames lists the names accepted in FIELD=REGEX.
var FieldNames = []string{fieldProject, fieldApp, fieldEnv, fieldHost, fieldURL}

// looksScoped finds a FIELD= prefix, known or not, so a typo such as
// "evn=prod" is reported instead of quietly matching nothing.
var looksScoped = regexp.MustCompile(`^([A-Za-z]+)=`)

// pattern is one --include or --exclude value.
type pattern struct {
	field string // fieldAny or one of FieldNames
	re    *regexp.Regexp
}

func (p pattern) match(f Fields) bool {
	switch p.field {
	case fieldProject:
		return p.re.MatchString(f.Project)
	case fieldApp:
		return p.re.MatchString(f.App)
	case fieldEnv:
		return p.re.MatchString(f.Env)
	case fieldHost:
		return p.re.MatchString(f.Host)
	case fieldURL:
		return p.re.MatchString(f.URL)
	}
	for _, value := range []string{f.Project, f.App, f.Env, f.Host, f.URL} {
		if p.re.MatchString(value) {
			return true
		}
	}
	return false
}

// Filter decides which instances are kept.
//
// A pattern is REGEX (matches anywhere in project, app, env, host or URL) or
// FIELD=REGEX (matches that field only). project=, app= and env= must match
// the whole name; host= and url= match anywhere.
//
// Includes of the same kind are alternatives, and different kinds must all
// match: "app=payments", "app=ledger", "env=prod" keeps payments or ledger,
// in prod. Any matching exclude then drops the instance.
type Filter struct {
	include map[string][]pattern // by field
	exclude []pattern
}

// New compiles the patterns (Go RE2 syntax).
func New(includes, excludes []string) (*Filter, error) {
	inc, err := compile("--include", includes)
	if err != nil {
		return nil, err
	}
	exc, err := compile("--exclude", excludes)
	if err != nil {
		return nil, err
	}
	f := &Filter{include: map[string][]pattern{}, exclude: exc}
	for _, p := range inc {
		f.include[p.field] = append(f.include[p.field], p)
	}
	return f, nil
}

func compile(flag string, values []string) ([]pattern, error) {
	var out []pattern
	for _, value := range values {
		field, expr := fieldAny, value
		if m := looksScoped.FindStringSubmatch(value); m != nil {
			field = strings.ToLower(m[1])
			if !isField(field) {
				return nil, fmt.Errorf("unknown %s field %q in %q (fields: %s; write [=] for a literal =)",
					flag, m[1], value, strings.Join(FieldNames, ", "))
			}
			expr = value[len(m[0]):]
		}
		if expr == "" {
			return nil, fmt.Errorf("empty %s pattern %q", flag, value)
		}
		if _, err := regexp.Compile(expr); err != nil {
			return nil, fmt.Errorf("invalid %s regex %q: %w", flag, value, err)
		}
		if field == fieldProject || field == fieldApp || field == fieldEnv {
			expr = "^(?:" + expr + ")$" // names match whole, so env=prod is not preprod
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("invalid %s regex %q: %w", flag, value, err)
		}
		out = append(out, pattern{field: field, re: re})
	}
	return out, nil
}

func isField(name string) bool {
	for _, known := range FieldNames {
		if name == known {
			return true
		}
	}
	return false
}

// Match reports whether an instance is kept.
func (f *Filter) Match(fields Fields) bool {
	for _, alternatives := range f.include {
		if !anyMatch(alternatives, fields) {
			return false
		}
	}
	return !anyMatch(f.exclude, fields)
}

func anyMatch(patterns []pattern, fields Fields) bool {
	for _, p := range patterns {
		if p.match(fields) {
			return true
		}
	}
	return false
}
