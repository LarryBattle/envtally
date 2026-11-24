// Package mockserver fakes the app types envtally supports, so its output can
// be checked without running real services. It does not depend on the
// envtally CLI: any HTTP client (curl, a browser) can call it.
//
// Every app instance has a fake host name, <app>.<env>.<instance>.<domain>,
// for example flask-catalog.dev.na1.example.com. A request names that host
// in one of three ways, and the rest of the URL is the endpoint:
//
//	http://flask-catalog.dev.na1.localhost:8099/health          (Host header)
//	http://127.0.0.1:8099/flask-catalog.dev.na1.example.com/health
//	http://127.0.0.1:8099/https://flask-catalog.dev.na1.example.com/health
//
// Only the first three labels (app, env, instance) are looked up, so any
// domain works. Anything that does not resolve is a 404.
//
// An app's name starts with its app type ("springboot-payments"); that
// prefix selects how the app answers. flask, rails and springboot are built
// in (one file each); a config can define more under "types".
//
// A config file (see default.json) holds named profiles. A profile lists the
// envs and the apps, every app is deployed to every instance of every env,
// and settings decide how each one answers: version, state, HTTP status,
// delay, custom responses.
package mockserver

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/LarryBattle/envtally/internal/config"
)

//go:embed default.json
var defaultJSON []byte

// DefaultJSON returns the built-in config file, as a starting point for a
// custom one.
func DefaultJSON() []byte { return defaultJSON }

// State is a typical condition of an app. Each app type answers it the way
// that kind of app really does.
type State string

// States.
const (
	Healthy State = "healthy" // health and info both answer normally (the default)
	Down    State = "down"    // the health endpoint reports a failure
	NoInfo  State = "noinfo"  // healthy, but the info endpoint does not exist (404)
	Partial State = "partial" // healthy, but info lacks a field the group key needs
)

// Config is a mock server config file.
type Config struct {
	DefaultProfile string `json:"defaultProfile"`
	// Domain ends the fake host names: <app>.<env>.<instance>.<domain>.
	// Default: example.com.
	Domain   string                `json:"domain,omitempty"`
	Envs     []Env                 `json:"envs,omitempty"`  // used by profiles without their own envs
	Types    map[string]CustomType `json:"types,omitempty"` // app types beyond the built-in ones
	Profiles map[string]Profile    `json:"profiles"`
}

// Profile is one selectable setup. Every app is deployed to every env.
type Profile struct {
	Description string         `json:"description,omitempty"`
	Envs        []Env          `json:"envs,omitempty"`
	Apps        map[string]App `json:"apps"`
}

// Env is one environment. Its settings apply to every app in it.
type Env struct {
	Name string `json:"name"`
	// Instances are the env's instances (clusters, regions, nodes): a count
	// (2 means na1 and na2) or a list of names. Default: one instance, na1.
	Instances InstanceList `json:"instances,omitempty"`
	Settings
}

// InstanceList is a list of instance names. In JSON it is a count or an
// array of names.
type InstanceList []string

// MaxInstances caps the count form of "instances".
const MaxInstances = 50

// UnmarshalJSON turns a count N into na1..naN.
func (l *InstanceList) UnmarshalJSON(data []byte) error {
	var count int
	if err := json.Unmarshal(data, &count); err == nil {
		if count < 1 || count > MaxInstances {
			return fmt.Errorf("instances must be between 1 and %d, got %d", MaxInstances, count)
		}
		*l = nil
		for i := 1; i <= count; i++ {
			*l = append(*l, fmt.Sprintf("na%d", i))
		}
		return nil
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return errors.New("instances must be a count (2) or a list of names ([\"na1\", \"na2\"])")
	}
	*l = names
	return nil
}

// App is one app. Its settings apply in every env; Overrides target one env
// ("prod") or one instance ("prod/na2").
type App struct {
	Settings
	Overrides map[string]Settings `json:"overrides,omitempty"`
}

// Settings decide how an instance answers. The most specific value wins:
// env, then app, then the app's env override, then its instance override.
type Settings struct {
	Version  string `json:"version,omitempty"`
	Revision string `json:"revision,omitempty"` // git commit id
	State    State  `json:"state,omitempty"`
	// Status forces this HTTP status on every endpoint, with the body that app
	// type typically sends for it (error page, redirect, ...).
	Status int `json:"status,omitempty"`
	// Delay is waited before every response, e.g. "600ms".
	Delay string `json:"delay,omitempty"`
	// Responses replaces single endpoints, keyed by path.
	Responses map[string]CustomResponse `json:"responses,omitempty"`
}

// CustomResponse is a response written in the config.
//
// Body is sent as-is when it is a JSON string, and as JSON otherwise. Without
// a body, the app type's typical response is kept and only the status,
// headers and delay given here are applied. ${app}, ${name}, ${type}, ${env},
// ${instance}, ${version} and ${revision} are replaced in the body.
type CustomResponse struct {
	Status      int               `json:"status,omitempty"`
	ContentType string            `json:"contentType,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        json.RawMessage   `json:"body,omitempty"`
	Delay       string            `json:"delay,omitempty"`
}

// CustomType is an app type defined in the config.
type CustomType struct {
	Description string `json:"description,omitempty"`
	// Profile is the envtally profile for this type; it goes into the
	// generated envtally config.
	Profile   config.Profile            `json:"profile"`
	Endpoints map[string]CustomResponse `json:"endpoints"`
	// States replaces endpoints while an instance is in that state.
	States map[State]map[string]CustomResponse `json:"states,omitempty"`
}

// Instance is one resolved app instance.
type Instance struct {
	App      string // e.g. springboot-payments
	Type     string // e.g. springboot
	Env      string
	Name     string // instance name, e.g. na1
	Domain   string
	Version  string
	Revision string
	State    State
	Status   int
	Delay    time.Duration

	responses map[string]CustomResponse
	// base is the path prefix the current request named the host with
	// ("/flask-catalog.dev.na1.example.com"); empty when the Host header did.
	base string
}

// Host is the instance's fake host name: <app>.<env>.<instance>.<domain>.
func (i Instance) Host() string {
	return i.App + "." + i.Env + "." + i.Name + "." + i.Domain
}

// loginPath is where a 3xx response sends the client.
func (i Instance) loginPath() string { return i.base + "/login" }

// Service is the app name without its type prefix: "payments".
func (i Instance) Service() string { return strings.TrimPrefix(i.App, i.Type+"-") }

// Default returns the built-in config.
func Default() *Config {
	cfg, err := Parse(defaultJSON)
	if err != nil {
		panic("mockserver: built-in config is invalid: " + err.Error())
	}
	return cfg
}

// Load reads and checks a config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes and checks config JSON. Unknown fields are rejected, and
// every profile must be usable.
func Parse(data []byte) (*Config, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("invalid mock config JSON: %w", err)
	}
	var problems []string
	if len(cfg.Profiles) == 0 {
		problems = append(problems, "profiles: at least one profile is required")
	}
	if _, ok := cfg.Profiles[cfg.DefaultProfile]; !ok && cfg.DefaultProfile != "" {
		problems = append(problems, fmt.Sprintf("defaultProfile: unknown profile %q", cfg.DefaultProfile))
	}
	problems = append(problems, checkTypes(&cfg)...)
	for _, name := range cfg.ProfileNames() {
		if _, err := New(&cfg, name); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return nil, errors.New("invalid mock config:\n  " + strings.Join(problems, "\n  "))
	}
	return &cfg, nil
}

// ProfileNames lists the profiles, sorted.
func (c *Config) ProfileNames() []string { return sortedKeys(c.Profiles) }

// merge returns s with every value set in o applied on top.
func (s Settings) merge(o Settings) Settings {
	if o.Version != "" {
		s.Version = o.Version
	}
	if o.Revision != "" {
		s.Revision = o.Revision
	}
	if o.State != "" {
		s.State = o.State
	}
	if o.Status != 0 {
		s.Status = o.Status
	}
	if o.Delay != "" {
		s.Delay = o.Delay
	}
	if len(o.Responses) > 0 {
		merged := map[string]CustomResponse{}
		for path, r := range s.Responses {
			merged[path] = r
		}
		for path, r := range o.Responses {
			merged[path] = r
		}
		s.Responses = merged
	}
	return s
}

// Names become host labels, so they cannot contain dots.
var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// checker collects problems under a path prefix such as profiles.drift.
type checker struct {
	prefix   string
	problems []string
}

func (c *checker) add(path, format string, args ...any) {
	c.problems = append(c.problems, c.prefix+path+": "+fmt.Sprintf(format, args...))
}

func (c *checker) err() error {
	if len(c.problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(c.problems, "\n  "))
}

func (c *checker) settings(path string, s Settings) {
	switch s.State {
	case "", Healthy, Down, NoInfo, Partial:
	default:
		c.add(path+".state", "unknown state %q (use healthy, down, noinfo or partial)", s.State)
	}
	c.status(path+".status", s.Status)
	c.delay(path+".delay", s.Delay)
	c.responses(path+".responses", s.Responses)
}

func (c *checker) responses(path string, responses map[string]CustomResponse) {
	for _, endpoint := range sortedKeys(responses) {
		r := responses[endpoint]
		if !strings.HasPrefix(endpoint, "/") {
			c.add(path, "endpoint %q must start with \"/\"", endpoint)
		}
		c.status(path+"."+endpoint+".status", r.Status)
		c.delay(path+"."+endpoint+".delay", r.Delay)
	}
}

func (c *checker) status(path string, status int) {
	if status != 0 && (status < 200 || status > 599) {
		c.add(path, "HTTP status must be between 200 and 599, got %d", status)
	}
}

func (c *checker) delay(path, value string) {
	if value == "" {
		return
	}
	if d, err := time.ParseDuration(value); err != nil || d < 0 {
		c.add(path, "invalid delay %q (examples: 300ms, 2s)", value)
	}
}

func parseDelay(value string) time.Duration {
	d, _ := time.ParseDuration(value)
	return d
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
