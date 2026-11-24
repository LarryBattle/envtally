package mockserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LarryBattle/envtally/internal/config"
)

// serve starts one profile of a config.
func serve(t *testing.T, cfg *Config, profile string) *httptest.Server {
	t.Helper()
	server, err := New(cfg, profile)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server)
	t.Cleanup(srv.Close)
	return srv
}

// request describes one GET. host, when set, replaces the Host header, as if
// the URL had named that host.
type request struct {
	url, host, accept string
}

// get sends the request without following redirects, so 3xx responses are seen.
func get(t *testing.T, rq request) (resp *http.Response, body string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rq.url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rq.host != "" {
		req.Host = rq.host
	}
	if rq.accept != "" {
		req.Header.Set("Accept", rq.accept)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(data)
}

// Every built-in envtally profile must have a built-in mock app type serving
// its health and info paths. This fails when a profile is added or changed
// without the mock.
func TestMockCoversBuiltinProfiles(t *testing.T) {
	types := map[string]*appType{}
	for _, typ := range builtinTypes() {
		types[typ.name] = typ
	}
	profiles := config.BuiltinProfiles()
	if len(types) != len(profiles) {
		t.Errorf("%d built-in mock app types, %d built-in profiles", len(types), len(profiles))
	}
	for name, profile := range profiles {
		typ, ok := types[name]
		if !ok {
			t.Errorf("no mock app type for built-in profile %q", name)
			continue
		}
		for _, want := range []string{profile.HealthPath, profile.InfoPath} {
			if _, ok := typ.endpoint(want); want != "" && !ok {
				t.Errorf("mock %q does not serve %s", name, want)
			}
		}
	}
}

// In every profile of the built-in config: every app is deployed to every
// instance of every env, each env has several instances, app names carry
// their type prefix, each built-in type is present, and the generated
// envtally config is valid.
func TestDefaultConfigProfiles(t *testing.T) {
	cfg := Default()
	if len(cfg.ProfileNames()) < 5 {
		t.Fatalf("profiles: %v", cfg.ProfileNames())
	}
	for _, name := range cfg.ProfileNames() {
		t.Run(name, func(t *testing.T) {
			server, err := New(cfg, name)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			typesSeen := map[string]bool{}
			for _, inst := range server.Instances() {
				seen[inst.Host()] = true
				typesSeen[inst.Type] = true
				if !strings.HasPrefix(inst.App, inst.Type+"-") {
					t.Errorf("app %q lacks its type prefix %q", inst.App, inst.Type)
				}
			}
			for _, app := range server.apps {
				for _, env := range server.envs {
					if len(env.Instances) < 2 {
						t.Errorf("env %s has %d instance(s), want several", env.Name, len(env.Instances))
					}
					for _, inst := range env.Instances {
						if host := app.name + "." + env.Name + "." + inst + ".example.com"; !seen[host] {
							t.Errorf("no instance %s", host)
						}
					}
				}
			}
			for _, typ := range builtinTypes() {
				if !typesSeen[typ.name] {
					t.Errorf("no %s app in this profile", typ.name)
				}
			}
			envtally, err := config.Parse(server.EnvtallyConfig("http://127.0.0.1:8099"))
			if err != nil {
				t.Fatal(err)
			}
			if issues := config.Validate(envtally); len(issues) > 0 {
				t.Errorf("generated envtally config has problems: %v", issues)
			}
			if got, want := len(envtally.Instances()), len(server.Instances()); got != want {
				t.Errorf("envtally config has %d instances, the server %d", got, want)
			}
		})
	}
}

// A fake host can be named by the Host header (any domain), or lead the path
// with or without a scheme. Every form reaches the same instance.
func TestURLForms(t *testing.T) {
	srv := serve(t, Default(), "healthy")
	const want = `{"status":"ok","checks":{"database":"ok"}}` + "\n"
	for _, inst := range []string{"na1", "na2"} {
		name := "flask-catalog.dev." + inst
		forms := []request{
			{url: srv.URL + "/health", host: name + ".127.0.0.1:8099"},
			{url: srv.URL + "/health", host: name + ".localhost:8099"},
			{url: srv.URL + "/health", host: name + ".127.0.0.1.nip.io:8099"},
			{url: srv.URL + "/health", host: name + ".example.com"},
			{url: srv.URL + "/health", host: strings.ToUpper(name) + ".Example.COM"},
			{url: srv.URL + "/" + name + ".example.com/health"},
			{url: srv.URL + "/" + name + ".example.com:8443/health"},
			{url: srv.URL + "/" + name + ".internal.corp/health"},
			{url: srv.URL + "/https://" + name + ".example.com/health"},
			{url: srv.URL + "/http://" + name + ".example.com/health"},
			{url: srv.URL + "/https:/" + name + ".example.com/health"},
		}
		for _, rq := range forms {
			resp, body := get(t, rq)
			if resp.StatusCode != 200 || body != want {
				t.Errorf("%s (Host %q): status %d, body %q", rq.url, rq.host, resp.StatusCode, body)
			}
		}
	}

	// Anything that names no instance is a 404 that lists what exists.
	misses := []request{
		{url: srv.URL + "/health"},
		{url: srv.URL + "/flask-catalog.dev.na9.example.com/health"},
		{url: srv.URL + "/flask-catalog.staging.na1.example.com/health"},
		{url: srv.URL + "/cobol-batch.dev.na1.example.com/health"},
		{url: srv.URL + "/flask-catalog.dev/health"},
		{url: srv.URL + "/health", host: "unknown.dev.na1.example.com"},
	}
	for _, rq := range misses {
		resp, body := get(t, rq)
		if resp.StatusCode != 404 || !strings.Contains(body, "no mock instance for host") || !strings.Contains(body, `"flask-catalog.dev.na1.example.com"`) {
			t.Errorf("%s (Host %q): status %d\n%s", rq.url, rq.host, resp.StatusCode, body)
		}
	}

	// A known host with an unknown endpoint gets that app type's own 404.
	resp, body := get(t, request{url: srv.URL + "/flask-catalog.dev.na1.example.com/nope"})
	if resp.StatusCode != 404 || !strings.Contains(body, "<title>404 Not Found</title>") {
		t.Errorf("unknown endpoint: status %d\n%s", resp.StatusCode, body)
	}
}

// A redirect points back into the form the request used.
func TestRedirectKeepsTheURLForm(t *testing.T) {
	srv := serve(t, Default(), "statuses")
	const host = "rails-storefront.prod.na2.example.com"
	tests := []struct {
		rq           request
		wantLocation string
	}{
		{request{url: srv.URL + "/up", host: host}, "/login"},
		{request{url: srv.URL + "/" + host + "/up"}, "/" + host + "/login"},
		{request{url: srv.URL + "/https://" + host + "/up"}, "/https://" + host + "/login"},
	}
	for _, tt := range tests {
		resp, _ := get(t, tt.rq)
		if got := resp.Header.Get("Location"); resp.StatusCode != 302 || got != tt.wantLocation {
			t.Errorf("%s: status %d, Location %q, want %q", tt.rq.url, resp.StatusCode, got, tt.wantLocation)
		}
		// The sign-in page it points to exists.
		login := request{url: srv.URL + tt.wantLocation, host: tt.rq.host}
		if resp, body := get(t, login); resp.StatusCode != 200 || !strings.Contains(body, "Sign in") {
			t.Errorf("%s: status %d", login.url, resp.StatusCode)
		}
	}
}

func TestResponses(t *testing.T) {
	tests := []struct {
		profile    string
		instance   string // <app>.<env>.<instance>
		endpoint   string
		wantStatus int
		wantType   string
		wantInBody []string
		wantHeader string // "Name: value", optional
	}{
		// Typical healthy answers, per app type and env.
		{"healthy", "springboot-payments.dev.na1", "/actuator/health", 200, contentActuator, []string{`"status": "UP"`, `"diskSpace"`}, ""},
		{"healthy", "springboot-payments.dev.na1", "/actuator/info", 200, contentActuator, []string{`"version": "1.5.0"`, `"id": "c3d4e5f"`, `"artifact": "payments"`}, ""},
		{"healthy", "springboot-ledger.test.na2", "/actuator/info", 200, contentActuator, []string{`"version": "1.4.2"`, `"artifact": "ledger"`}, ""},
		{"healthy", "rails-storefront.test.na1", "/up", 200, contentHTML, []string{"background-color: green"}, ""},
		{"healthy", "rails-storefront.test.na1", "/version", 200, contentJSON, []string{`"version":"1.4.2"`, `"revision":"a1b2c3d"`}, ""},
		{"healthy", "flask-catalog.dev.na1", "/health", 200, contentJSON, []string{`"status":"ok"`}, ""},
		{"healthy", "flask-catalog.prod.na3", "/info", 200, contentJSON, []string{`"name":"catalog"`, `"version":"1.4.1"`}, ""},
		// One instance of an env differs from its neighbors.
		{"drift", "springboot-payments.prod.na1", "/actuator/info", 200, contentActuator, []string{`"version": "1.4.1"`}, ""},
		{"drift", "springboot-payments.prod.na2", "/actuator/info", 200, contentActuator, []string{`"version": "1.4.2"`}, ""},
		{"drift", "springboot-payments.prod.na3", "/actuator/info", 200, contentActuator, []string{`"version": "1.4.1"`}, ""},
		// States.
		{"outage", "springboot-payments.prod.na2", "/actuator/health", 503, contentActuator, []string{`"status": "DOWN"`, "CannotGetJdbcConnectionException"}, ""},
		{"outage", "springboot-payments.prod.na1", "/actuator/health", 200, contentActuator, []string{`"status": "UP"`}, ""},
		{"outage", "rails-storefront.test.na1", "/up", 500, contentHTML, []string{"background-color: red"}, ""},
		{"outage", "flask-catalog.dev.na2", "/health", 200, contentJSON, []string{`"status":"ok"`}, ""},
		{"outage", "flask-catalog.dev.na2", "/info", 404, contentHTML, []string{"<title>404 Not Found</title>"}, ""},
		{"outage", "springboot-ledger.test.na1", "/actuator/info", 200, contentActuator, []string{`"version": "1.4.2"`}, ""},
		// Forced statuses: 2xx, 3xx, 4xx and 5xx, with each type's usual body.
		{"statuses", "flask-catalog.dev.na1", "/health", 202, contentJSON, []string{`"status":"ok"`}, ""},
		{"statuses", "flask-catalog.test.na1", "/health", 404, contentHTML, []string{"<h1>Not Found</h1>"}, ""},
		{"statuses", "rails-storefront.dev.na1", "/up", 500, contentHTML, []string{"We're sorry, but something went wrong (500)"}, ""},
		{"statuses", "rails-storefront.prod.na2", "/up", 302, contentHTML, []string{"You are being <a href"}, "Location: /rails-storefront.prod.na2.example.com/login"},
		{"statuses", "springboot-ledger.test.na1", "/actuator/health", 401, contentJSON, []string{`"error": "Unauthorized"`}, `WWW-Authenticate: Basic realm="Realm"`},
		{"statuses", "springboot-payments.dev.na1", "/actuator/info", 503, contentJSON, []string{`"error": "Service Unavailable"`, `"path": "/actuator/info"`}, ""},
		{"statuses", "springboot-payments.test.na1", "/actuator/health", 302, "", nil, "Location: /springboot-payments.test.na1.example.com/login"},
		{"statuses", "springboot-payments.prod.na1", "/actuator/health", 200, contentActuator, []string{`"status": "UP"`}, ""},
		// A config-defined app type and custom responses.
		{"custom", "node-gateway.dev.na1", "/healthz", 200, contentJSON, []string{`"ok": true`}, ""},
		{"custom", "node-gateway.dev.na1", "/meta", 200, contentJSON, []string{`"name": "gateway"`, `"version": "1.5.0"`, `"sha": "c3d4e5f"`}, ""},
		{"custom", "node-gateway.test.na1", "/healthz", 503, contentJSON, []string{`"ok": false`}, ""},
		{"custom", "node-gateway.dev.na1", "/nope", 404, contentJSON, []string{`"error":"Not Found"`}, ""},
		{"custom", "springboot-payments.test.na1", "/actuator/health", 503, contentJSON, []string{`"status": "OUT_OF_SERVICE"`}, "Retry-After: 120"},
		{"custom", "springboot-payments.prod.na2", "/actuator/info", 500, contentJSON, []string{`"error": "Internal Server Error"`}, ""},
		{"custom", "flask-catalog.dev.na1", "/info", 200, contentHTML, []string{"<h1>catalog is down for maintenance</h1>"}, ""},
		{"healthy", "springboot-payments.dev.na1", "/actuator/metrics", 404, contentJSON, []string{`"error": "Not Found"`}, ""},
	}
	cfg := Default()
	servers := map[string]*httptest.Server{}
	for _, tt := range tests {
		if servers[tt.profile] == nil {
			servers[tt.profile] = serve(t, cfg, tt.profile)
		}
		t.Run(tt.profile+"/"+tt.instance+tt.endpoint, func(t *testing.T) {
			resp, body := get(t, request{url: servers[tt.profile].URL + "/" + tt.instance + ".example.com" + tt.endpoint})
			contentType := resp.Header.Get("Content-Type")
			if resp.StatusCode != tt.wantStatus || contentType != tt.wantType {
				t.Errorf("got %d %q, want %d %q", resp.StatusCode, contentType, tt.wantStatus, tt.wantType)
			}
			for _, part := range tt.wantInBody {
				if !strings.Contains(body, part) {
					t.Errorf("body lacks %q:\n%s", part, body)
				}
			}
			if name, value, ok := strings.Cut(tt.wantHeader, ": "); ok && resp.Header.Get(name) != value {
				t.Errorf("header %s = %q, want %q", name, resp.Header.Get(name), value)
			}
			if strings.Contains(contentType, "json") && !json.Valid([]byte(body)) {
				t.Errorf("body is not valid JSON:\n%s", body)
			}
		})
	}
}

// A delay slows only the targeted instance.
func TestDelayTargetsOneInstance(t *testing.T) {
	srv := serve(t, Default(), "slow")
	timed := func(host string) time.Duration {
		start := time.Now()
		if resp, _ := get(t, request{url: srv.URL + "/" + host + "/up"}); resp.StatusCode != 200 {
			t.Errorf("%s: status %d", host, resp.StatusCode)
		}
		return time.Since(start)
	}
	if d := timed("rails-storefront.prod.na2.example.com"); d < 600*time.Millisecond {
		t.Errorf("delayed instance answered in %s, want at least 600ms", d)
	}
	if d := timed("rails-storefront.prod.na1.example.com"); d > 500*time.Millisecond {
		t.Errorf("the other instance took %s", d)
	}
}

// The index is JSON for curl and a page of links for a browser, and every
// URL it lists is answered by an instance.
func TestIndex(t *testing.T) {
	srv := serve(t, Default(), "healthy")
	resp, body := get(t, request{url: srv.URL + "/"})
	var doc struct {
		Profile, Domain string
		Instances       []struct {
			Host string
			URLs []string
		}
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil || resp.StatusCode != 200 {
		t.Fatalf("status %d, err %v\n%s", resp.StatusCode, err, body)
	}
	// 4 apps x (2 dev + 2 test + 3 prod) instances.
	if doc.Profile != "healthy" || doc.Domain != "example.com" || len(doc.Instances) != 28 {
		t.Errorf("profile %q, domain %q, %d instances", doc.Profile, doc.Domain, len(doc.Instances))
	}
	for _, inst := range doc.Instances {
		for _, url := range inst.URLs {
			if resp, body := get(t, request{url: url}); resp.StatusCode != 200 {
				t.Errorf("%s: status %d\n%s", url, resp.StatusCode, body)
			}
		}
	}

	resp, body = get(t, request{url: srv.URL + "/", accept: "text/html,application/xhtml+xml"})
	link := `<a href="` + srv.URL + `/flask-catalog.dev.na1.example.com/health">`
	if resp.Header.Get("Content-Type") != contentHTML || !strings.Contains(body, link) {
		t.Errorf("browser index lacks %s:\n%s", link, body)
	}
}

// A user's own config: their domain and envs, instances as a count and as
// names, an app type they define, and a custom response for one endpoint.
func TestCustomConfigFile(t *testing.T) {
	cfg, err := Parse([]byte(`{
		"defaultProfile": "mine",
		"domain": "corp.test",
		"types": {
			"django": {
				"profile": {"healthPath": "/ht/", "infoPath": "/about", "groupKey": "${release}"},
				"endpoints": {
					"/ht/": {"body": {"DatabaseBackend": "working"}},
					"/about": {"body": {"release": "${version}", "env": "${env}", "node": "${instance}", "host": "${host}"}}
				}
			}
		},
		"profiles": {
			"mine": {
				"envs": [
					{"name": "qa", "version": "7.1", "instances": ["east", "west"]},
					{"name": "live", "version": "7.0", "instances": 4},
					{"name": "lab", "version": "8.0"}
				],
				"apps": {
					"django-portal": {
						"overrides": {"live/na4": {"responses": {"/ht/": {"status": 418, "body": "teapot\n"}}}}
					},
					"flask-search": {"delay": "1ms"}
				}
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(server.Instances()); got != 2*(2+4+1) {
		t.Errorf("%d instances, want 14", got)
	}
	srv := httptest.NewServer(server)
	t.Cleanup(srv.Close)

	resp, body := get(t, request{url: srv.URL + "/django-portal.qa.west.corp.test/about"})
	for _, part := range []string{`"release": "7.1"`, `"node": "west"`, `"env": "qa"`, `"host": "django-portal.qa.west.corp.test"`} {
		if resp.StatusCode != 200 || !strings.Contains(body, part) {
			t.Errorf("status %d, body lacks %s\n%s", resp.StatusCode, part, body)
		}
	}
	resp, body = get(t, request{url: srv.URL + "/ht/", host: "django-portal.live.na4.corp.test"})
	if resp.StatusCode != 418 || body != "teapot\n" || resp.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("status %d %q %q", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	if resp, _ := get(t, request{url: srv.URL + "/ht/", host: "django-portal.live.na3.corp.test"}); resp.StatusCode != 200 {
		t.Errorf("neighbor instance: status %d", resp.StatusCode)
	}
	// An env without "instances" has one, na1.
	if resp, body := get(t, request{url: srv.URL + "/flask-search.lab.na1.corp.test/info"}); resp.StatusCode != 200 || !strings.Contains(body, `"version":"8.0"`) {
		t.Errorf("status %d\n%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(server.EnvtallyConfig("http://127.0.0.1:8099")), `"http://127.0.0.1:8099/django-portal.live.na4.corp.test"`) {
		t.Errorf("envtally config lacks the fake host URLs")
	}
}

func TestParseRejectsBadConfigs(t *testing.T) {
	tests := []struct {
		name, json, wantErr string
	}{
		{"not JSON", `{`, "invalid mock config JSON"},
		{"unknown field", `{"profile": {}}`, "unknown field"},
		{"no profiles", `{}`, "at least one profile is required"},
		{"unknown default", `{"defaultProfile": "x", "profiles": {"a": {"envs": [{"name": "dev"}], "apps": {"flask-a": {}}}}}`, `unknown profile "x"`},
		{"no envs", `{"profiles": {"a": {"apps": {"flask-a": {}}}}}`, "profiles.a.envs: at least one env is required"},
		{"no type prefix", `{"profiles": {"a": {"envs": [{"name": "dev"}], "apps": {"payments": {}}}}}`, `profiles.a.apps.payments: app names are "<app type>-<name>"`},
		{"dotted app name", `{"profiles": {"a": {"envs": [{"name": "dev"}], "apps": {"flask-a.b": {}}}}}`, "without dots"},
		{"dotted env name", `{"profiles": {"a": {"envs": [{"name": "dev.eu"}], "apps": {"flask-a": {}}}}}`, `invalid name "dev.eu"`},
		{"zero instances", `{"profiles": {"a": {"envs": [{"name": "dev", "instances": 0}], "apps": {"flask-a": {}}}}}`, "instances must be between 1 and 50"},
		{"bad instances", `{"profiles": {"a": {"envs": [{"name": "dev", "instances": "two"}], "apps": {"flask-a": {}}}}}`, "instances must be a count"},
		{"duplicate instance", `{"profiles": {"a": {"envs": [{"name": "dev", "instances": ["x", "x"]}], "apps": {"flask-a": {}}}}}`, `duplicate instance "x"`},
		{"bad state", `{"profiles": {"a": {"envs": [{"name": "dev"}], "apps": {"flask-a": {"state": "broken"}}}}}`, `unknown state "broken"`},
		{"bad status", `{"profiles": {"a": {"envs": [{"name": "dev", "status": 99}], "apps": {"flask-a": {}}}}}`, "HTTP status must be between 200 and 599"},
		{"bad delay", `{"profiles": {"a": {"envs": [{"name": "dev"}], "apps": {"flask-a": {"delay": "soon"}}}}}`, `invalid delay "soon"`},
		{"override of unknown env", `{"profiles": {"a": {"envs": [{"name": "dev"}], "apps": {"flask-a": {"overrides": {"prod": {}}}}}}}`, "overrides.prod: no such env or env/instance"},
		{"override of unknown instance", `{"profiles": {"a": {"envs": [{"name": "dev", "instances": 2}], "apps": {"flask-a": {"overrides": {"dev/na3": {}}}}}}}`, "overrides.dev/na3: no such env or env/instance"},
		{"redefined built-in type", `{"types": {"rails": {"profile": {"healthPath": "/up", "groupKey": "${v}"}, "endpoints": {"/up": {}}}}, "profiles": {"a": {"envs": [{"name": "dev"}], "apps": {"flask-a": {}}}}}`, "built-in app type"},
		{"type health path without endpoint", `{"types": {"node": {"profile": {"healthPath": "/healthz", "groupKey": "${v}"}, "endpoints": {"/meta": {}}}}, "profiles": {"a": {"envs": [{"name": "dev"}], "apps": {"node-a": {}}}}}`, "types.node.profile.healthPath: must be one of this type's endpoints"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.json))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestNewUnknownProfile(t *testing.T) {
	if _, err := New(Default(), "nope"); err == nil || !strings.Contains(err.Error(), "available: custom, drift, healthy") {
		t.Errorf("error = %v", err)
	}
}
