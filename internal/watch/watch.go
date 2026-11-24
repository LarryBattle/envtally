// Package watch re-runs a check until everything is in sync or time runs out.
package watch

import (
	"context"
	"fmt"
	"time"

	"github.com/LarryBattle/envtally/internal/group"
)

// Clock is the time source; tests inject a fake one.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// RealClock uses the time package.
type RealClock struct{}

// Now returns the current time.
func (RealClock) Now() time.Time { return time.Now() }

// After waits like time.After.
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Why a watch ended.
const (
	ReasonSynced    = "synced"
	ReasonTimeout   = "timeout"
	ReasonCancelled = "cancelled"
)

// Options controls the loop.
type Options struct {
	Interval time.Duration
	Timeout  time.Duration
	Clock    Clock
}

// Tick describes one completed check.
type Tick struct {
	Number  int // 1-based
	At      time.Time
	Result  group.Result
	Changes []string // differences from the previous tick; nil on the first
}

// Outcome is the end state of a watch.
type Outcome struct {
	Reason  string
	Final   group.Result
	Checks  int
	Elapsed time.Duration
}

// Run calls check every Interval and reports each tick to onTick. It stops
// when the result is OK, when Timeout elapses, or when ctx is cancelled.
func Run(ctx context.Context, opt Options, check func(context.Context) group.Result, onTick func(Tick)) Outcome {
	clock := opt.Clock
	if clock == nil {
		clock = RealClock{}
	}
	start := clock.Now()
	var out Outcome
	finish := func(reason string) Outcome {
		out.Reason = reason
		out.Elapsed = clock.Now().Sub(start)
		return out
	}
	for {
		result := check(ctx)
		if ctx.Err() != nil && out.Checks > 0 {
			// A check cut short by Ctrl-C reports false failures; keep the last full one.
			return finish(ReasonCancelled)
		}
		tick := Tick{Number: out.Checks + 1, At: clock.Now(), Result: result}
		if out.Checks > 0 {
			tick.Changes = Diff(out.Final, result)
		}
		out.Final = result
		out.Checks++
		onTick(tick)

		if result.OK() {
			return finish(ReasonSynced)
		}
		if ctx.Err() != nil {
			return finish(ReasonCancelled)
		}
		remaining := opt.Timeout - clock.Now().Sub(start)
		if remaining <= 0 {
			return finish(ReasonTimeout)
		}
		select {
		case <-ctx.Done():
			return finish(ReasonCancelled)
		case <-clock.After(min(opt.Interval, remaining)):
		}
		if clock.Now().Sub(start) >= opt.Timeout {
			return finish(ReasonTimeout)
		}
	}
}

// Diff lists what changed between two results: instance state flips, group
// key changes and env state changes.
func Diff(prev, cur group.Result) []string {
	type snapshot struct{ state, key string }
	before := map[string]snapshot{}
	envBefore := map[string]string{}
	for _, e := range prev.Envs {
		envBefore[e.Name()] = e.State
		for _, inst := range e.Instances {
			before[e.Name()+" "+inst.URL] = snapshot{inst.State, inst.Key}
		}
	}
	var changes []string
	for _, e := range cur.Envs {
		for _, inst := range e.Instances {
			old, seen := before[e.Name()+" "+inst.URL]
			if !seen {
				continue
			}
			if old.state != inst.State {
				line := fmt.Sprintf("%s %s: %s -> %s", e.Name(), inst.Host, old.state, inst.State)
				if inst.Detail != "" {
					line += " (" + inst.Detail + ")"
				}
				changes = append(changes, line)
			}
			if old.key != inst.Key && old.key != "" && inst.Key != "" {
				changes = append(changes, fmt.Sprintf("%s %s: key %s -> %s", e.Name(), inst.Host, old.key, inst.Key))
			}
		}
		if old, seen := envBefore[e.Name()]; seen && old != e.State {
			changes = append(changes, fmt.Sprintf("%s: %s -> %s", e.Name(), old, e.State))
		}
	}
	return changes
}
