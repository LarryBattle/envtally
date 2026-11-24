package config

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/LarryBattle/envtally/internal/tmpl"
)

// Issue is one validation problem at a JSON path such as
// $.projects.billing.apps.payments.envs.dev[1].
type Issue struct {
	Path    string
	Message string
}

func (i Issue) String() string { return i.Path + ": " + i.Message }

type validator struct {
	cfg    *Config
	issues []Issue
}

func (v *validator) add(path, format string, args ...any) {
	v.issues = append(v.issues, Issue{Path: path, Message: fmt.Sprintf(format, args...)})
}

// Validate returns every semantic problem in the config, sorted by path.
func Validate(c *Config) []Issue {
	v := &validator{cfg: c}
	v.defaults()
	v.vars()
	v.links("$.templates", c.Templates)
	for _, name := range sortedKeys(c.Profiles) {
		v.profile("$.profiles."+name, c.Profiles[name])
	}
	if len(c.Projects) == 0 {
		v.add("$.projects", "at least one project is required")
	}
	for _, name := range sortedKeys(c.Projects) {
		v.project("$.projects."+name, c.Projects[name])
	}
	sort.SliceStable(v.issues, func(i, j int) bool { return v.issues[i].Path < v.issues[j].Path })
	return v.issues
}

func (v *validator) defaults() {
	d := v.cfg.Defaults
	if !slices.Contains(Outputs, d.Output) {
		v.add("$.defaults.output", "unknown output %q (use %s)", d.Output, strings.Join(Outputs, ", "))
	}
	if d.Concurrency < 1 {
		v.add("$.defaults.concurrency", "must be 1 or more")
	}
	if d.RetryCount() < 0 {
		v.add("$.defaults.retries", "must be 0 or more")
	}
	v.duration("$.defaults.timeout", d.Timeout)
	v.duration("$.defaults.slowThreshold", d.SlowThreshold)
	if d.Profile != "" {
		if _, ok := v.cfg.Profiles[d.Profile]; !ok {
			v.add("$.defaults.profile", "unknown profile %q", d.Profile)
		}
	}
}

func (v *validator) duration(path, value string) {
	d, err := time.ParseDuration(value)
	if err != nil {
		v.add(path, "invalid duration %q (examples: 500ms, 5s, 1m)", value)
	} else if d <= 0 {
		v.add(path, "duration must be greater than zero")
	}
}

func (v *validator) vars() {
	for _, name := range sortedKeys(v.cfg.Vars) {
		path := "$.vars." + name
		if slices.Contains(BuiltinVars, name) {
			v.add(path, "%q is a built-in variable and cannot be redefined", name)
		}
		if strings.HasPrefix(name, "env:") {
			v.add(path, "variable names cannot start with \"env:\"")
		}
		if strings.Contains(v.cfg.Vars[name], "${") {
			v.add(path, "vars are literal values; ${...} inside a var is not expanded")
		}
	}
}

// links checks link templates (swagger, jenkins, ...): only built-in and
// user vars are allowed.
func (v *validator) links(path string, templates map[string]string) {
	for _, name := range sortedKeys(templates) {
		refs, err := tmpl.Refs(templates[name])
		if err != nil {
			v.add(path+"."+name, "%v", err)
			continue
		}
		for _, ref := range refs {
			if _, ok := v.cfg.Vars[ref]; !ok && !slices.Contains(BuiltinVars, ref) {
				v.add(path+"."+name, "undefined variable ${%s}", ref)
			}
		}
	}
}

func (v *validator) profile(path string, p Profile) {
	if !strings.HasPrefix(p.HealthPath, "/") {
		v.add(path+".healthPath", "required, and must start with \"/\"")
	}
	if p.InfoPath != "" && !strings.HasPrefix(p.InfoPath, "/") {
		v.add(path+".infoPath", "must start with \"/\"")
	}
	if p.HealthyWhen != nil && p.HealthyWhen.Field == "" {
		v.add(path+".healthyWhen.field", "required")
	}
	// No groupKey means a status-only profile.
	if p.GroupKey != "" {
		refs, err := tmpl.Refs(p.GroupKey)
		switch {
		case err != nil:
			v.add(path+".groupKey", "%v", err)
		case len(refs) == 0:
			v.add(path+".groupKey", "must reference at least one response field such as ${version}; "+
				"leave groupKey out to only check for an HTTP 2xx status")
		}
	}
	for _, name := range sortedKeys(p.Headers) {
		refs, err := tmpl.Refs(p.Headers[name])
		if err != nil {
			v.add(path+".headers."+name, "%v", err)
			continue
		}
		for _, ref := range refs {
			if envName, isEnv := strings.CutPrefix(ref, "env:"); isEnv {
				if envName == "" {
					v.add(path+".headers."+name, "${env:} needs a variable name")
				}
				continue
			}
			if _, ok := v.cfg.Vars[ref]; !ok {
				v.add(path+".headers."+name, "undefined variable ${%s} (headers may use vars and ${env:NAME})", ref)
			}
		}
	}
}

func (v *validator) project(path string, p Project) {
	v.links(path+".templates", p.Templates)
	if len(p.Apps) == 0 {
		v.add(path+".apps", "at least one app is required")
	}
	for _, name := range sortedKeys(p.Apps) {
		v.app(path+".apps."+name, p.Apps[name])
	}
}

func (v *validator) app(path string, a App) {
	v.links(path+".templates", a.Templates)
	switch profile := v.cfg.profileName(a); {
	case profile == "":
		v.add(path+".profile", "no profile: set it here or in defaults.profile")
	case !v.cfg.hasProfile(profile):
		v.add(path+".profile", "unknown profile %q", profile)
	}
	if len(a.Envs) == 0 {
		v.add(path+".envs", "at least one env is required")
	}
	for _, env := range sortedKeys(a.Envs) {
		envPath := path + ".envs." + env
		urls := a.Envs[env]
		if len(urls) == 0 {
			v.add(envPath, "at least one instance URL is required")
		}
		seen := map[string]bool{}
		for i, raw := range urls {
			itemPath := fmt.Sprintf("%s[%d]", envPath, i)
			if msg := checkURL(raw); msg != "" {
				v.add(itemPath, "%s", msg)
			}
			if seen[raw] {
				v.add(itemPath, "duplicate URL %q", raw)
			}
			seen[raw] = true
		}
	}
}

func checkURL(raw string) string {
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return fmt.Sprintf("invalid URL %q: %v", raw, err)
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Sprintf("invalid URL %q: scheme must be http or https", raw)
	case u.Host == "":
		return fmt.Sprintf("invalid URL %q: missing host", raw)
	case u.RawQuery != "" || u.Fragment != "":
		return fmt.Sprintf("invalid URL %q: must not contain a query or fragment", raw)
	}
	return ""
}

func (c *Config) hasProfile(name string) bool {
	_, ok := c.Profiles[name]
	return ok
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
