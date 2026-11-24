// Package group builds group keys and decides sync/drift per app+env.
package group

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/LarryBattle/envtally/internal/config"
	"github.com/LarryBattle/envtally/internal/probe"
	"github.com/LarryBattle/envtally/internal/tmpl"
)

// Instance states.
const (
	InstUp     = "UP"     // healthy, and its group key (if wanted) was read
	InstDown   = "DOWN"   // health check failed
	InstNoInfo = "NOINFO" // healthy, but the group key source could not be read
)

// Env (app+env) states.
const (
	EnvInSync  = "IN SYNC" // all instances up with one complete, identical key
	EnvDrift   = "DRIFT"   // all up, more than one distinct key
	EnvDown    = "DOWN"    // at least one instance is not up
	EnvUnknown = "UNKNOWN" // all up, but a key has missing fields ("?")
	EnvUp      = "UP"      // all healthy, keys not compared (--only health, or a status-only profile)
)

// MissingField replaces a group-key field absent from the response.
const MissingField = "?"

// Instance is the evaluated state of one instance.
type Instance struct {
	Project, App, Env string
	URL               string
	Host              string // display name: host[:port] plus the URL path, if any
	Profile           string
	State             string
	Detail            string // why it is not UP
	Key               string // group key; empty when not evaluated
	KeyComplete       bool   // false when a key field was missing
	Health, Info      time.Duration
	Total             time.Duration // Health + Info
	Slow              bool          // Total exceeded slowThreshold
}

// KeyCount is a distinct group key and how many instances have it.
type KeyCount struct {
	Key   string
	Count int
}

// Env is the evaluated state of one app+env.
type Env struct {
	Project, App, Env string
	State             string
	Keys              []KeyCount // most common first
	Instances         []Instance
	Min, Avg, Max     time.Duration // over UP instances
}

// Name returns project/app/env.
func (e Env) Name() string { return e.Project + "/" + e.App + "/" + e.Env }

// Result is one complete check.
type Result struct {
	Envs         []Env
	KeyTemplates map[string]string // profile -> group-key template, for profiles in use
	Only         string
}

// OK reports whether every env is in sync (or up, with --only health).
func (r Result) OK() bool {
	for _, e := range r.Envs {
		if e.State != EnvInSync && e.State != EnvUp {
			return false
		}
	}
	return true
}

// Build turns probe results into a Result.
func Build(results []probe.Result, cfg *config.Config, only string) Result {
	res := Result{Only: only, KeyTemplates: map[string]string{}}
	slow := cfg.Defaults.SlowThresholdDuration()
	instances := make([]Instance, 0, len(results))
	for _, r := range results {
		profile := cfg.Profiles[r.Target.Instance.Profile]
		if only != probe.OnlyHealth && profile.GroupKey != "" {
			res.KeyTemplates[r.Target.Instance.Profile] = profile.GroupKey
		}
		instances = append(instances, Classify(r, profile, only, slow))
	}
	res.Envs = Evaluate(instances, only)
	return res
}

// Classify decides one instance's state and group key.
func Classify(r probe.Result, p config.Profile, only string, slowThreshold time.Duration) Instance {
	t := r.Target.Instance
	inst := Instance{Project: t.Project, App: t.App, Env: t.Env, URL: t.URL, Host: t.Label, Profile: t.Profile}
	if r.Health != nil {
		inst.Health = r.Health.Duration
	}
	if r.Info != nil {
		inst.Info = r.Info.Duration
	}
	inst.Total = inst.Health + inst.Info
	inst.Slow = slowThreshold > 0 && inst.Total > slowThreshold
	inst.State, inst.Detail, inst.Key, inst.KeyComplete = classify(r, p, only)
	return inst
}

func classify(r probe.Result, p config.Profile, only string) (state, detail, key string, complete bool) {
	if only != probe.OnlyInfo {
		if why := unhealthy(r.Health, p.HealthyWhen); why != "" {
			return InstDown, why, "", false
		}
		if only == probe.OnlyHealth {
			return InstUp, "", "", false
		}
	}
	source, label := r.Health, "health"
	if p.InfoPath != "" {
		source, label = r.Info, "info"
		if why := callFailure(source); why != "" {
			return InstNoInfo, "info: " + why, "", false
		}
	}
	if p.GroupKey == "" { // status-only profile: 2xx is all that is asked
		return InstUp, "", "", false
	}
	var doc any
	if err := json.Unmarshal(source.Body, &doc); err != nil {
		return InstNoInfo, label + " response is not JSON, cannot build group key", "", false
	}
	key, complete = Key(p.GroupKey, doc)
	return InstUp, "", key, complete
}

func callFailure(c *probe.Call) string {
	switch {
	case c == nil:
		return "not requested"
	case c.Err != "":
		return c.Err
	case !c.OK():
		return fmt.Sprintf("HTTP %d", c.Status)
	}
	return ""
}

// unhealthy returns why a health call does not count as healthy, or "".
func unhealthy(c *probe.Call, when *config.HealthyWhen) string {
	if why := callFailure(c); why != "" {
		return why
	}
	if when == nil {
		return ""
	}
	var doc any
	if err := json.Unmarshal(c.Body, &doc); err != nil {
		return "health response is not JSON"
	}
	got, ok := Lookup(doc, when.Field)
	switch {
	case !ok:
		return fmt.Sprintf("health field %q is missing", when.Field)
	case got != when.Equals:
		return fmt.Sprintf("%s=%s (want %s)", when.Field, got, when.Equals)
	}
	return ""
}

// Key renders a group-key template against a JSON document. A missing field
// renders as "?" and makes the key incomplete.
func Key(template string, doc any) (key string, complete bool) {
	return tmpl.ExpandLoose(template, func(path string) (string, bool) { return Lookup(doc, path) }, MissingField)
}

// Evaluate groups instances by project/app/env (keeping order) and decides
// each env's state.
func Evaluate(instances []Instance, only string) []Env {
	var envs []Env
	index := map[string]int{}
	for _, inst := range instances {
		name := inst.Project + "/" + inst.App + "/" + inst.Env
		i, ok := index[name]
		if !ok {
			i = len(envs)
			index[name] = i
			envs = append(envs, Env{Project: inst.Project, App: inst.App, Env: inst.Env})
		}
		envs[i].Instances = append(envs[i].Instances, inst)
	}
	for i := range envs {
		finish(&envs[i], only)
	}
	return envs
}

func finish(e *Env, only string) {
	counts := map[string]int{}
	allUp, allComplete := true, true
	var total time.Duration
	up := 0
	for _, inst := range e.Instances {
		if inst.State != InstUp {
			allUp = false
			continue
		}
		up++
		total += inst.Total
		if up == 1 || inst.Total < e.Min {
			e.Min = inst.Total
		}
		e.Max = max(e.Max, inst.Total)
		if inst.Key == "" { // not evaluated: --only health, or a status-only profile
			continue
		}
		counts[inst.Key]++
		allComplete = allComplete && inst.KeyComplete
	}
	if up > 0 {
		e.Avg = total / time.Duration(up)
	}
	for key, n := range counts {
		e.Keys = append(e.Keys, KeyCount{Key: key, Count: n})
	}
	sort.Slice(e.Keys, func(i, j int) bool {
		if e.Keys[i].Count != e.Keys[j].Count {
			return e.Keys[i].Count > e.Keys[j].Count
		}
		return e.Keys[i].Key < e.Keys[j].Key
	})
	switch {
	case !allUp:
		e.State = EnvDown
	case only == probe.OnlyHealth || len(counts) == 0:
		e.State = EnvUp
	case !allComplete:
		e.State = EnvUnknown
	case len(e.Keys) > 1:
		e.State = EnvDrift
	default:
		e.State = EnvInSync
	}
}
