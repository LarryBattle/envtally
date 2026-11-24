package group

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/LarryBattle/envtally/internal/config"
	"github.com/LarryBattle/envtally/internal/probe"
)

func decode(t *testing.T, s string) any {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestLookup(t *testing.T) {
	doc := decode(t, `{
		"status": "UP", "port": 8080, "ready": true, "nothing": null, "blank": "",
		"build": {"version": "1.4.2"},
		"git": {"commit": {"id": "a1b2c3d"}},
		"flat.key": "flat", "git.branch": "main",
		"tags": ["a", "b"]}`)
	tests := []struct {
		path   string
		want   string
		wantOK bool
	}{
		{"status", "UP", true},
		{"build.version", "1.4.2", true},
		{"git.commit.id", "a1b2c3d", true},
		{"flat.key", "flat", true},
		{"git.branch", "main", true},
		{"port", "8080", true},
		{"ready", "true", true},
		{"tags", `["a","b"]`, true},
		{"nothing", "", false},
		{"blank", "", false},
		{"build.missing", "", false},
		{"status.deeper", "", false},
	}
	for _, tt := range tests {
		got, ok := Lookup(doc, tt.path)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("Lookup(%q) = %q, %v; want %q, %v", tt.path, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestKeyMissingFieldsRenderAsQuestionMark(t *testing.T) {
	doc := decode(t, `{"build": {"version": "1.4.2"}}`)
	tests := []struct {
		template string
		want     string
		complete bool
	}{
		{"${build.version}", "1.4.2", true},
		{"${build.version}|${git.commit.id}", "1.4.2|?", false},
		{"${a}|${b}", "?|?", false},
	}
	for _, tt := range tests {
		got, complete := Key(tt.template, doc)
		if got != tt.want || complete != tt.complete {
			t.Errorf("Key(%q) = %q, %v; want %q, %v", tt.template, got, complete, tt.want, tt.complete)
		}
	}
}

func call(status int, body string) *probe.Call {
	return &probe.Call{Status: status, Body: []byte(body), Duration: 100 * time.Millisecond}
}

func TestClassify(t *testing.T) {
	spring := config.BuiltinProfiles()["springboot"]
	rails := config.BuiltinProfiles()["rails"]
	noInfo := config.Profile{HealthPath: "/health", GroupKey: "${version}"}
	statusOnly := config.Profile{HealthPath: "/login"}
	statusOnlyInfo := config.Profile{HealthPath: "/login", InfoPath: "/api"}
	info := `{"build": {"version": "1.4.2"}, "git": {"commit": {"id": "a1b2"}}}`

	tests := []struct {
		name      string
		profile   config.Profile
		only      string
		health    *probe.Call
		info      *probe.Call
		wantState string
		wantKey   string
		complete  bool
	}{
		{"healthy with key", spring, "", call(200, `{"status":"UP"}`), call(200, info), InstUp, "1.4.2|a1b2", true},
		{"2xx but field says DOWN", spring, "", call(200, `{"status":"DOWN"}`), nil, InstDown, "", false},
		{"2xx but field missing", spring, "", call(200, `{}`), nil, InstDown, "", false},
		{"2xx but body not JSON", spring, "", call(200, `OK`), nil, InstDown, "", false},
		{"503 with UP body", spring, "", call(503, `{"status":"UP"}`), nil, InstDown, "", false},
		{"connection error", spring, "", &probe.Call{Err: "connection refused"}, nil, InstDown, "", false},
		{"no healthyWhen: any 2xx is healthy", rails, "", call(204, ``), call(200, `{"version":"2","revision":"r9"}`), InstUp, "2|r9", true},
		{"info failed", spring, "", call(200, `{"status":"UP"}`), call(404, ``), InstNoInfo, "", false},
		{"info not JSON", spring, "", call(200, `{"status":"UP"}`), call(200, `<html>`), InstNoInfo, "", false},
		{"info lacks a field", spring, "", call(200, `{"status":"UP"}`), call(200, `{"build":{"version":"1.4.2"}}`), InstUp, "1.4.2|?", false},
		{"no infoPath: key from health", noInfo, "", call(200, `{"version":"7"}`), nil, InstUp, "7", true},
		{"no infoPath, health not JSON", noInfo, "", call(200, `ok`), nil, InstNoInfo, "", false},
		{"status only: any 2xx body is up", statusOnly, "", call(200, `<html>`), nil, InstUp, "", false},
		{"status only: non-2xx is down", statusOnly, "", call(403, `<html>`), nil, InstDown, "", false},
		{"status only with info: both 2xx", statusOnlyInfo, "", call(200, `<html>`), call(200, `plain`), InstUp, "", false},
		{"status only with info: info fails", statusOnlyInfo, "", call(200, `<html>`), call(500, ``), InstNoInfo, "", false},
		{"only health: no key", spring, probe.OnlyHealth, call(200, `{"status":"UP"}`), nil, InstUp, "", false},
		{"only info: health skipped", spring, probe.OnlyInfo, nil, call(200, info), InstUp, "1.4.2|a1b2", true},
		{"only info: info down", spring, probe.OnlyInfo, nil, &probe.Call{Err: "timeout"}, InstNoInfo, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(probe.Result{Health: tt.health, Info: tt.info}, tt.profile, tt.only, time.Second)
			if got.State != tt.wantState || got.Key != tt.wantKey || got.KeyComplete != tt.complete {
				t.Errorf("got state=%s key=%q complete=%v (%s); want state=%s key=%q complete=%v",
					got.State, got.Key, got.KeyComplete, got.Detail, tt.wantState, tt.wantKey, tt.complete)
			}
			if got.State != InstUp && got.Detail == "" {
				t.Error("a non-UP instance must explain why in Detail")
			}
		})
	}
}

func TestClassifyTimingAndSlow(t *testing.T) {
	r := probe.Result{
		Health: &probe.Call{Status: 200, Body: []byte(`{"status":"UP"}`), Duration: 700 * time.Millisecond},
		Info:   &probe.Call{Status: 200, Body: []byte(`{}`), Duration: 500 * time.Millisecond},
	}
	got := Classify(r, config.BuiltinProfiles()["springboot"], "", time.Second)
	if got.Total != 1200*time.Millisecond || !got.Slow {
		t.Errorf("Total = %v, Slow = %v; want 1.2s, true", got.Total, got.Slow)
	}
}

func up(key string, total time.Duration) Instance {
	return Instance{Project: "p", App: "a", Env: "dev", State: InstUp, Key: key, KeyComplete: true, Total: total}
}

func TestEvaluate(t *testing.T) {
	down := Instance{Project: "p", App: "a", Env: "dev", State: InstDown}
	partial := Instance{Project: "p", App: "a", Env: "dev", State: InstUp, Key: "?|?"}
	tests := []struct {
		name      string
		instances []Instance
		only      string
		wantState string
		wantKeys  []KeyCount
	}{
		{"all same", []Instance{up("1", 0), up("1", 0)}, "", EnvInSync, []KeyCount{{"1", 2}}},
		{"single instance", []Instance{up("1", 0)}, "", EnvInSync, []KeyCount{{"1", 1}}},
		{"drift, most common first", []Instance{up("2", 0), up("1", 0), up("1", 0)}, "", EnvDrift, []KeyCount{{"1", 2}, {"2", 1}}},
		{"down beats sync", []Instance{up("1", 0), down}, "", EnvDown, []KeyCount{{"1", 1}}},
		{"down beats drift", []Instance{up("1", 0), up("2", 0), down}, "", EnvDown, []KeyCount{{"1", 1}, {"2", 1}}},
		{"identical incomplete keys are not in sync", []Instance{partial, partial}, "", EnvUnknown, []KeyCount{{"?|?", 2}}},
		{"status only: up, nothing compared", []Instance{up("", 0), up("", 0)}, "", EnvUp, nil},
		{"status only with a down instance", []Instance{up("", 0), down}, "", EnvDown, nil},
		{"only health", []Instance{up("", 0), up("", 0)}, probe.OnlyHealth, EnvUp, nil},
		{"only health with a down instance", []Instance{up("", 0), down}, probe.OnlyHealth, EnvDown, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envs := Evaluate(tt.instances, tt.only)
			if len(envs) != 1 {
				t.Fatalf("got %d envs, want 1", len(envs))
			}
			e := envs[0]
			if e.State != tt.wantState {
				t.Errorf("state = %s, want %s", e.State, tt.wantState)
			}
			if len(e.Keys) != len(tt.wantKeys) {
				t.Fatalf("keys = %v, want %v", e.Keys, tt.wantKeys)
			}
			for i := range e.Keys {
				if e.Keys[i] != tt.wantKeys[i] {
					t.Errorf("keys = %v, want %v", e.Keys, tt.wantKeys)
				}
			}
			wantOK := tt.wantState == EnvInSync || tt.wantState == EnvUp
			if got := (Result{Envs: envs}).OK(); got != wantOK {
				t.Errorf("OK = %v, want %v", got, wantOK)
			}
		})
	}
}

func TestEvaluateGroupsAndTimes(t *testing.T) {
	other := up("1", 50*time.Millisecond)
	other.Env = "prod"
	envs := Evaluate([]Instance{up("1", 110*time.Millisecond), other, up("1", 95*time.Millisecond)}, "")
	if len(envs) != 2 || envs[0].Env != "dev" || envs[1].Env != "prod" || len(envs[0].Instances) != 2 {
		t.Fatalf("grouping wrong: %+v", envs)
	}
	e := envs[0]
	if e.Min != 95*time.Millisecond || e.Max != 110*time.Millisecond || e.Avg != 102500*time.Microsecond {
		t.Errorf("min/avg/max = %v/%v/%v", e.Min, e.Avg, e.Max)
	}
}
