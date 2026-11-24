package mockserver

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/LarryBattle/envtally/internal/config"
)

const (
	contentJSON = "application/json"
	contentHTML = "text/html; charset=utf-8"

	// A fixed clock keeps responses identical from run to run.
	buildTime = "2026-11-18T09:30:00Z"
	now       = "2026-11-20T12:00:00Z"
)

// endpoint is one route of an app type.
type endpoint struct {
	path        string // starts with "/", e.g. /actuator/health
	description string
	respond     func(Instance) Response
}

// appType is how one kind of app answers: its endpoints for each state, and
// the page it typically sends for any other HTTP status.
type appType struct {
	name        string
	description string
	profile     config.Profile // the envtally profile that reads this type
	custom      bool           // defined in the config, not built in
	endpoints   []endpoint
	errorPage   func(inst Instance, status int, path string) Response
}

func (t *appType) endpoint(path string) (endpoint, bool) {
	for _, ep := range t.endpoints {
		if ep.path == path {
			return ep, true
		}
	}
	return endpoint{}, false
}

// builtinTypes are the app types envtally ships a profile for. To add one,
// write its file and list it here.
func builtinTypes() []*appType {
	types := []*appType{flask(), rails(), springboot()}
	profiles := config.BuiltinProfiles()
	for _, t := range types {
		t.profile = profiles[t.name]
	}
	return types
}

// allTypes returns the built-in and config-defined types by name.
func allTypes(cfg *Config) map[string]*appType {
	types := map[string]*appType{}
	for _, t := range builtinTypes() {
		types[t.name] = t
	}
	for name, ct := range cfg.Types {
		if _, builtin := types[name]; !builtin {
			types[name] = customType(name, ct)
		}
	}
	return types
}

// typeFor finds an app's type from its name prefix: "springboot-payments" is
// a springboot app. The longest matching type name wins.
func typeFor(types map[string]*appType, app string) *appType {
	var best *appType
	for name, t := range types {
		if strings.HasPrefix(app, name+"-") && len(app) > len(name)+1 && (best == nil || len(name) > len(best.name)) {
			best = t
		}
	}
	return best
}

// checkTypes validates the config-defined types.
func checkTypes(cfg *Config) []string {
	c := &checker{prefix: "types."}
	builtin := map[string]bool{}
	for _, t := range builtinTypes() {
		builtin[t.name] = true
	}
	for _, name := range sortedKeys(cfg.Types) {
		ct := cfg.Types[name]
		switch {
		case builtin[name]:
			c.add(name, "%q is a built-in app type and cannot be redefined", name)
		case !nameRE.MatchString(name):
			c.add(name, "invalid name (use lowercase letters, digits, '_' and '-')")
		}
		if len(ct.Endpoints) == 0 {
			c.add(name+".endpoints", "at least one endpoint is required")
		}
		c.responses(name+".endpoints", ct.Endpoints)
		for _, state := range []State{Healthy, Down, NoInfo, Partial} {
			c.responses(name+".states."+string(state), ct.States[state])
		}
		for state := range ct.States {
			if state != Healthy && state != Down && state != NoInfo && state != Partial {
				c.add(name+".states", "unknown state %q (use healthy, down, noinfo or partial)", state)
			}
		}
		if _, ok := ct.Endpoints[ct.Profile.HealthPath]; !ok {
			c.add(name+".profile.healthPath", "must be one of this type's endpoints")
		}
		if _, ok := ct.Endpoints[ct.Profile.InfoPath]; !ok && ct.Profile.InfoPath != "" {
			c.add(name+".profile.infoPath", "must be one of this type's endpoints")
		}
		if ct.Profile.GroupKey == "" {
			c.add(name+".profile.groupKey", "required")
		}
	}
	return c.problems
}

// customType builds an app type from its config definition.
func customType(name string, ct CustomType) *appType {
	t := &appType{name: name, description: ct.Description, profile: ct.Profile, custom: true, errorPage: genericErrorPage}
	for _, path := range sortedKeys(ct.Endpoints) {
		t.endpoints = append(t.endpoints, endpoint{path: path, respond: func(inst Instance) Response {
			if r, ok := ct.States[inst.State][path]; ok {
				return render(r, inst)
			}
			if inst.State == NoInfo && path == ct.Profile.InfoPath {
				return genericErrorPage(inst, http.StatusNotFound, path)
			}
			return render(ct.Endpoints[path], inst)
		}})
	}
	return t
}

// genericErrorPage is the plain JSON error config-defined types send.
func genericErrorPage(inst Instance, status int, path string) Response {
	resp := jsonResponse(status, fmt.Sprintf(`{"status":%d,"error":%q,"path":%q}`+"\n", status, http.StatusText(status), path))
	return redirectTo(resp, inst)
}

func jsonResponse(status int, body string) Response {
	return Response{Status: status, ContentType: contentJSON, Body: body}
}

// redirectTo adds the Location header to a 3xx response: the instance's
// sign-in page, as an app behind a login would send.
func redirectTo(resp Response, inst Instance) Response {
	if resp.Status/100 == 3 {
		resp.Headers = mergeHeaders(resp.Headers, map[string]string{"Location": inst.loginPath()})
	}
	return resp
}
