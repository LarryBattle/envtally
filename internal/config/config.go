// Package config loads, normalizes and validates the envtally JSON config.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Default values applied when the config leaves a field out.
const (
	DefaultOutput        = "table"
	DefaultConcurrency   = 10
	DefaultTimeout       = "5s"
	DefaultRetries       = 1
	DefaultSlowThreshold = "1s"
)

// Outputs lists the valid output styles.
var Outputs = []string{"table", "tree", "compact", "markdown", "csv", "json"}

// BuiltinVars are the variables available in link templates.
var BuiltinVars = []string{"project", "app", "env", "host", "base_url"}

// Config is the whole config file.
type Config struct {
	Schema    string             `json:"$schema,omitempty"`
	Defaults  Defaults           `json:"defaults"`
	EnvOrder  []string           `json:"envOrder,omitempty"`
	Vars      map[string]string  `json:"vars,omitempty"`
	Templates map[string]string  `json:"templates,omitempty"`
	Profiles  map[string]Profile `json:"profiles"`
	Projects  map[string]Project `json:"projects"`
}

// Defaults are the global settings. Flags override them.
type Defaults struct {
	Output        string `json:"output"`
	Profile       string `json:"profile,omitempty"`
	Concurrency   int    `json:"concurrency"`
	Timeout       string `json:"timeout"`
	Retries       *int   `json:"retries"`
	Insecure      bool   `json:"insecure"`
	SlowThreshold string `json:"slowThreshold"`
}

// TimeoutDuration returns the per-request timeout (valid after Validate).
func (d Defaults) TimeoutDuration() time.Duration { return mustDuration(d.Timeout) }

// SlowThresholdDuration returns the slow marker threshold (valid after Validate).
func (d Defaults) SlowThresholdDuration() time.Duration { return mustDuration(d.SlowThreshold) }

// RetryCount returns the number of retries on connection errors.
func (d Defaults) RetryCount() int {
	if d.Retries == nil {
		return DefaultRetries
	}
	return *d.Retries
}

func mustDuration(s string) time.Duration {
	d, _ := time.ParseDuration(s)
	return d
}

// Profile describes one app type: where to probe and how to build the group key.
// Without a GroupKey the profile is status-only: each endpoint just has to
// answer HTTP 2xx, and no versions are compared.
type Profile struct {
	Description string            `json:"description,omitempty"`
	HealthPath  string            `json:"healthPath"`
	InfoPath    string            `json:"infoPath,omitempty"`
	HealthyWhen *HealthyWhen      `json:"healthyWhen,omitempty"`
	GroupKey    string            `json:"groupKey,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}

// HealthyWhen requires a field of the health JSON to equal a value.
type HealthyWhen struct {
	Field  string `json:"field"`
	Equals string `json:"equals"`
}

// Project groups apps.
type Project struct {
	Description string            `json:"description,omitempty"`
	Templates   map[string]string `json:"templates,omitempty"`
	Apps        map[string]App    `json:"apps"`
}

// App is one deployable with instances per environment.
type App struct {
	Description string                `json:"description,omitempty"`
	Profile     string                `json:"profile,omitempty"`
	Templates   map[string]string     `json:"templates,omitempty"`
	Envs        map[string]StringList `json:"envs"`
}

// StringList accepts a JSON string or an array of strings.
type StringList []string

// UnmarshalJSON normalizes "x" to ["x"].
func (l *StringList) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*l = StringList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return errors.New("env value must be a URL string or an array of URL strings")
	}
	*l = many
	return nil
}

// BuiltinProfiles returns the profiles available without declaring them.
// A profile of the same name in the config replaces the built-in one.
func BuiltinProfiles() map[string]Profile {
	return map[string]Profile{
		"springboot": {
			Description: "Spring Boot Actuator (built-in)",
			HealthPath:  "/actuator/health",
			InfoPath:    "/actuator/info",
			HealthyWhen: &HealthyWhen{Field: "status", Equals: "UP"},
			GroupKey:    "${build.version}|${git.commit.id}",
		},
		"rails": {
			Description: "Rails /up plus a JSON /version endpoint (built-in)",
			HealthPath:  "/up",
			InfoPath:    "/version",
			GroupKey:    "${version}|${revision}",
		},
		"flask": {
			Description: "Flask with JSON /health and /info (built-in)",
			HealthPath:  "/health",
			InfoPath:    "/info",
			HealthyWhen: &HealthyWhen{Field: "status", Equals: "ok"},
			GroupKey:    "${version}",
		},
	}
}

// Load reads and normalizes a config file. Call Validate on the result.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// CommentPrefix marks notes for people: an object key that starts with it
// (".comment", ".comment-headers", ...) is ignored wherever it appears.
const CommentPrefix = ".comment"

// Parse decodes and normalizes config JSON. Comment keys are dropped; any
// other unknown field is rejected so typos do not pass silently.
func Parse(data []byte) (*Config, error) {
	data, err := stripComments(data)
	if err != nil {
		return nil, fmt.Errorf("invalid config JSON: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("invalid config JSON: %w", err)
	}
	c.normalize()
	return &c, nil
}

// stripComments removes every comment key from the JSON document.
func stripComments(data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("unexpected data after the top-level object")
	}
	return json.Marshal(withoutComments(doc))
}

func withoutComments(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for key, value := range v {
			if strings.HasPrefix(key, CommentPrefix) {
				delete(v, key)
			} else {
				v[key] = withoutComments(value)
			}
		}
	case []any:
		for i, value := range v {
			v[i] = withoutComments(value)
		}
	}
	return v
}

func (c *Config) normalize() {
	d := &c.Defaults
	if d.Output == "" {
		d.Output = DefaultOutput
	}
	if d.Concurrency == 0 {
		d.Concurrency = DefaultConcurrency
	}
	if d.Timeout == "" {
		d.Timeout = DefaultTimeout
	}
	if d.Retries == nil {
		n := DefaultRetries
		d.Retries = &n
	}
	if d.SlowThreshold == "" {
		d.SlowThreshold = DefaultSlowThreshold
	}
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	for name, p := range BuiltinProfiles() {
		if _, ok := c.Profiles[name]; !ok {
			c.Profiles[name] = p
		}
	}
	for _, project := range c.Projects {
		for _, app := range project.Apps {
			for _, urls := range app.Envs {
				for i, u := range urls {
					urls[i] = strings.TrimRight(strings.TrimSpace(u), "/")
				}
			}
		}
	}
}
