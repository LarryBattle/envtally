package watch

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/LarryBattle/envtally/internal/group"
)

// fakeClock jumps forward instantly whenever someone waits.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.now = c.now.Add(d)
	ch := make(chan time.Time, 1)
	ch <- c.now
	return ch
}

func result(envState, instState, key string) group.Result {
	return group.Result{Envs: []group.Env{{
		Project: "p", App: "a", Env: "dev", State: envState,
		Instances: []group.Instance{{URL: "https://h1", Host: "h1", State: instState, Key: key}},
	}}}
}

func TestRun(t *testing.T) {
	drift := result(group.EnvDrift, group.InstUp, "1")
	synced := result(group.EnvInSync, group.InstUp, "2")
	tests := []struct {
		name        string
		results     []group.Result // one per check; the last repeats
		wantReason  string
		wantChecks  int
		wantElapsed time.Duration
	}{
		{"already in sync: one check, no waiting", []group.Result{synced}, ReasonSynced, 1, 0},
		{"syncs on the third check", []group.Result{drift, drift, synced}, ReasonSynced, 3, 20 * time.Second},
		{"never syncs: timeout", []group.Result{drift}, ReasonTimeout, 6, time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := &fakeClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
			calls, ticks := 0, 0
			check := func(context.Context) group.Result {
				r := tt.results[min(calls, len(tt.results)-1)]
				calls++
				return r
			}
			// 10s interval, 55s timeout: checks at 0,10,..,50, then the last wait is cut to 5s.
			opt := Options{Interval: 10 * time.Second, Timeout: 55 * time.Second, Clock: clock}
			if tt.wantReason == ReasonTimeout {
				opt.Timeout = time.Minute
				tt.wantChecks = 6
			}
			out := Run(context.Background(), opt, check, func(Tick) { ticks++ })
			if out.Reason != tt.wantReason || out.Checks != tt.wantChecks || out.Elapsed != tt.wantElapsed {
				t.Errorf("got %s after %d checks, %v; want %s after %d checks, %v",
					out.Reason, out.Checks, out.Elapsed, tt.wantReason, tt.wantChecks, tt.wantElapsed)
			}
			if ticks != out.Checks {
				t.Errorf("onTick called %d times for %d checks", ticks, out.Checks)
			}
			if out.Final.OK() != (tt.wantReason == ReasonSynced) {
				t.Errorf("Final.OK() = %v", out.Final.OK())
			}
		})
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	checks := 0
	check := func(context.Context) group.Result {
		checks++
		if checks == 2 {
			cancel() // Ctrl-C arrives during the second check
		}
		return result(group.EnvDown, group.InstDown, "")
	}
	out := Run(ctx, Options{Interval: time.Millisecond, Timeout: time.Hour}, check, func(Tick) {})
	if out.Reason != ReasonCancelled || out.Checks != 1 {
		t.Errorf("got %s after %d checks; want cancelled with the interrupted check discarded", out.Reason, out.Checks)
	}
}

func TestTicksCarryOnlyChanges(t *testing.T) {
	results := []group.Result{
		result(group.EnvDown, group.InstDown, ""),
		result(group.EnvDown, group.InstDown, ""),
		result(group.EnvInSync, group.InstUp, "1.4.2"),
	}
	calls := 0
	check := func(context.Context) group.Result { calls++; return results[calls-1] }
	var changes [][]string
	Run(context.Background(), Options{Interval: time.Second, Timeout: time.Minute, Clock: &fakeClock{}}, check,
		func(tk Tick) { changes = append(changes, tk.Changes) })
	want := [][]string{nil, nil, {"p/a/dev h1: DOWN -> UP", "p/a/dev: DOWN -> IN SYNC"}}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("changes = %v, want %v", changes, want)
	}
}

func TestDiff(t *testing.T) {
	downDetail := result(group.EnvDown, group.InstDown, "")
	downDetail.Envs[0].Instances[0].Detail = "HTTP 503"
	tests := []struct {
		name      string
		prev, cur group.Result
		want      []string
	}{
		{"no change", result(group.EnvDrift, group.InstUp, "1"), result(group.EnvDrift, group.InstUp, "1"), nil},
		{"key change", result(group.EnvDrift, group.InstUp, "1"), result(group.EnvDrift, group.InstUp, "2"),
			[]string{"p/a/dev h1: key 1 -> 2"}},
		{"key change and sync", result(group.EnvDrift, group.InstUp, "1"), result(group.EnvInSync, group.InstUp, "2"),
			[]string{"p/a/dev h1: key 1 -> 2", "p/a/dev: DRIFT -> IN SYNC"}},
		{"goes down with reason", result(group.EnvInSync, group.InstUp, "1"), downDetail,
			[]string{"p/a/dev h1: UP -> DOWN (HTTP 503)", "p/a/dev: IN SYNC -> DOWN"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Diff(tt.prev, tt.cur); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
