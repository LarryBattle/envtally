package report

import (
	"encoding/json"
	"io"

	"github.com/LarryBattle/envtally/internal/group"
)

type jsonResult struct {
	OK                bool              `json:"ok"`
	Only              string            `json:"only,omitempty"`
	GroupKeyTemplates map[string]string `json:"groupKeyTemplates"`
	Envs              []jsonEnv         `json:"envs"`
}

type jsonEnv struct {
	Project   string         `json:"project"`
	App       string         `json:"app"`
	Env       string         `json:"env"`
	Status    string         `json:"status"`
	Keys      []jsonKey      `json:"keys"`
	MinMs     int64          `json:"minMs"`
	AvgMs     int64          `json:"avgMs"`
	MaxMs     int64          `json:"maxMs"`
	Instances []jsonInstance `json:"instances"`
}

type jsonKey struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type jsonInstance struct {
	URL         string `json:"url"`
	Host        string `json:"host"`
	Profile     string `json:"profile"`
	Status      string `json:"status"`
	Detail      string `json:"detail,omitempty"`
	GroupKey    string `json:"groupKey"`
	KeyComplete bool   `json:"keyComplete"`
	HealthMs    int64  `json:"healthMs"`
	InfoMs      int64  `json:"infoMs"`
	TotalMs     int64  `json:"totalMs"`
	Slow        bool   `json:"slow"`
}

// renderJSON: the full structured result, times in raw milliseconds.
func renderJSON(w io.Writer, r group.Result) error {
	out := jsonResult{OK: r.OK(), Only: r.Only, GroupKeyTemplates: r.KeyTemplates, Envs: []jsonEnv{}}
	if out.GroupKeyTemplates == nil {
		out.GroupKeyTemplates = map[string]string{}
	}
	for _, e := range r.Envs {
		je := jsonEnv{
			Project: e.Project, App: e.App, Env: e.Env, Status: e.State,
			Keys:  []jsonKey{},
			MinMs: e.Min.Milliseconds(), AvgMs: e.Avg.Milliseconds(), MaxMs: e.Max.Milliseconds(),
		}
		for _, k := range e.Keys {
			je.Keys = append(je.Keys, jsonKey{Key: k.Key, Count: k.Count})
		}
		for _, inst := range e.Instances {
			je.Instances = append(je.Instances, jsonInstance{
				URL: inst.URL, Host: inst.Host, Profile: inst.Profile,
				Status: inst.State, Detail: inst.Detail,
				GroupKey: inst.Key, KeyComplete: inst.KeyComplete,
				HealthMs: inst.Health.Milliseconds(), InfoMs: inst.Info.Milliseconds(),
				TotalMs: inst.Total.Milliseconds(), Slow: inst.Slow,
			})
		}
		out.Envs = append(out.Envs, je)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
