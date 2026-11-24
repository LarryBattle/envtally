package config

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/LarryBattle/envtally/internal/tmpl"
)

// Instance is one URL of one app in one env, with its resolved profile.
type Instance struct {
	Project string
	App     string
	Env     string
	URL     string // base URL, no trailing slash
	Host    string // host[:port] of URL
	Label   string // Host plus the URL path, if any; tells instances behind one host apart
	Profile string
}

// Instances flattens the config. Order: project and app by name, envs by
// envOrder (then by name), URLs as written.
func (c *Config) Instances() []Instance {
	var out []Instance
	for _, projectName := range sortedKeys(c.Projects) {
		project := c.Projects[projectName]
		for _, appName := range sortedKeys(project.Apps) {
			app := project.Apps[appName]
			for _, env := range c.sortEnvs(sortedKeys(app.Envs)) {
				for _, raw := range app.Envs[env] {
					host, label := "", raw
					if u, err := url.Parse(raw); err == nil {
						host, label = u.Host, u.Host+u.Path
					}
					out = append(out, Instance{
						Project: projectName, App: appName, Env: env,
						URL: raw, Host: host, Label: label, Profile: c.profileName(app),
					})
				}
			}
		}
	}
	return out
}

func (c *Config) profileName(a App) string {
	if a.Profile != "" {
		return a.Profile
	}
	return c.Defaults.Profile
}

// sortEnvs puts envs listed in envOrder first (in that order); the rest
// keep their alphabetical order.
func (c *Config) sortEnvs(envs []string) []string {
	rank := func(env string) int {
		if i := slices.Index(c.EnvOrder, env); i >= 0 {
			return i
		}
		return len(c.EnvOrder)
	}
	sort.SliceStable(envs, func(i, j int) bool { return rank(envs[i]) < rank(envs[j]) })
	return envs
}

// Links expands the link templates (swagger, jenkins, ...) for an instance.
// Override order: global templates, then project, then app.
func (c *Config) Links(inst Instance) (map[string]string, error) {
	project := c.Projects[inst.Project]
	app := project.Apps[inst.App]
	merged := map[string]string{}
	for _, layer := range []map[string]string{c.Templates, project.Templates, app.Templates} {
		for name, template := range layer {
			merged[name] = template
		}
	}
	builtins := map[string]string{
		"project": inst.Project, "app": inst.App, "env": inst.Env,
		"host": inst.Host, "base_url": inst.URL,
	}
	out := map[string]string{}
	for name, template := range merged {
		value, err := tmpl.Expand(template, tmpl.Maps(builtins, c.Vars))
		if err != nil {
			return nil, fmt.Errorf("template %q for %s/%s/%s: %w", name, inst.Project, inst.App, inst.Env, err)
		}
		out[name] = value
	}
	return out, nil
}

// Headers expands a profile's request headers. Values may use vars and
// ${env:NAME}; an unset OS environment variable is an error.
func (c *Config) Headers(profile string, getenv func(string) (string, bool)) (map[string]string, error) {
	out := map[string]string{}
	for name, template := range c.Profiles[profile].Headers {
		unset := ""
		value, err := tmpl.Expand(template, func(ref string) (string, bool) {
			if envName, isEnv := strings.CutPrefix(ref, "env:"); isEnv {
				v, ok := getenv(envName)
				if !ok {
					unset = envName
				}
				return v, ok
			}
			v, ok := c.Vars[ref]
			return v, ok
		})
		if unset != "" {
			return nil, fmt.Errorf("profile %q header %q needs the environment variable %s, which is not set.\n"+
				"Set it, then run again:\n  PowerShell: $env:%s = \"value\"\n  bash/zsh:   export %s=value",
				profile, name, unset, unset, unset)
		}
		if err != nil {
			return nil, fmt.Errorf("profile %q header %q: %w", profile, name, err)
		}
		out[name] = value
	}
	return out, nil
}
