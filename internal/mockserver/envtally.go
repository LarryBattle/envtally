package mockserver

import (
	"encoding/json"

	"github.com/LarryBattle/envtally/internal/config"
)

// EnvtallyConfig returns an envtally config that checks every app of this
// profile on a mock server at baseURL. Instance URLs name the fake host in
// the path (<baseURL>/<fake host>), which needs no DNS setup. Built-in app
// types use envtally's built-in profiles; config-defined types bring their own.
func (s *Server) EnvtallyConfig(baseURL string) []byte {
	type app struct {
		Profile string              `json:"profile"`
		Envs    map[string][]string `json:"envs"`
	}
	type project struct {
		Description string         `json:"description"`
		Apps        map[string]app `json:"apps"`
	}
	doc := struct {
		EnvOrder []string                  `json:"envOrder"`
		Profiles map[string]config.Profile `json:"profiles,omitempty"`
		Projects map[string]project        `json:"projects"`
	}{Profiles: map[string]config.Profile{}}
	for _, env := range s.envs {
		doc.EnvOrder = append(doc.EnvOrder, env.Name)
	}
	apps := map[string]app{}
	for _, a := range s.apps {
		if a.typ.custom {
			doc.Profiles[a.typ.name] = a.typ.profile
		}
		envs := map[string][]string{}
		for _, inst := range a.instances {
			envs[inst.Env] = append(envs[inst.Env], baseURL+"/"+inst.Host())
		}
		apps[a.name] = app{Profile: a.typ.name, Envs: envs}
	}
	doc.Projects = map[string]project{
		"mock": {Description: "mock-proxy-server, profile " + s.Profile + ": " + s.Description, Apps: apps},
	}
	out, _ := json.MarshalIndent(doc, "", "  ")
	return append(out, '\n')
}
