package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// minimal builds a config around one app; extra is spliced in at the top level.
func minimal(extraTop, appBody string) string {
	if appBody == "" {
		appBody = `"envs": {"dev": "https://a.example.com"}`
	}
	if extraTop != "" {
		extraTop += ","
	}
	return `{` + extraTop + `"projects": {"p": {"apps": {"a": {` + appBody + `}}}}}`
}

func mustParse(t *testing.T, data string) *Config {
	t.Helper()
	c, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return c
}

func TestParseNormalizes(t *testing.T) {
	c := mustParse(t, minimal(`"defaults": {"profile": "flask"}`,
		`"envs": {"dev": ["https://a.example.com/", "https://b.example.com"], "prod": "https://p.example.com"}`))

	envs := c.Projects["p"].Apps["a"].Envs
	if got, want := envs["prod"], (StringList{"https://p.example.com"}); !reflect.DeepEqual(got, want) {
		t.Errorf("string env not normalized to array: %v", got)
	}
	if got := envs["dev"][0]; got != "https://a.example.com" {
		t.Errorf("trailing slash not trimmed: %q", got)
	}
	d := c.Defaults
	if d.Output != "table" || d.Concurrency != 10 || d.Timeout != "5s" || d.RetryCount() != 1 || d.SlowThreshold != "1s" {
		t.Errorf("defaults not applied: %+v", d)
	}
	for _, name := range []string{"springboot", "rails", "flask"} {
		if _, ok := c.Profiles[name]; !ok {
			t.Errorf("built-in profile %q missing", name)
		}
	}
	if issues := Validate(c); len(issues) != 0 {
		t.Errorf("unexpected issues: %v", issues)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ name, data string }{
		{"not json", `{`},
		{"unknown field (typo)", `{"projectz": {}}`},
		{"env is a number", minimal("", `"envs": {"dev": 5}`)},
		{"trailing data", `{"projects": {}} {}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.data)); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

// Keys starting with .comment are notes: dropped anywhere, even inside maps
// whose keys are otherwise names (envs, headers, vars).
func TestCommentsAreIgnored(t *testing.T) {
	c := mustParse(t, `{
		".comment": "top", ".comment-2": ["several", "lines"],
		"defaults": {".comment": "x", "profile": "x", "retries": 0},
		"vars": {".comment": "x", "team": "billing"},
		"templates": {".comment": "x", "home": "${base_url}/"},
		"profiles": {".comment": "x", "x": {".comment-healthPath": "x", "healthPath": "/h",
			"healthyWhen": {".comment": "x", "field": "s", "equals": "UP"},
			"headers": {".comment": "x", "X-Team": "${team}"}}},
		"projects": {".comment": "x", "p": {".comment": "x", "apps": {".comment": "x", "a": {
			".comment": "x", "envs": {".comment": "x", "dev": "https://a.example.com"}}}}}}`)
	if issues := Validate(c); len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
	if n := len(c.Instances()); n != 1 {
		t.Errorf("instances = %d, want 1 (a comment became an env?)", n)
	}
	headers, err := c.Headers("x", func(string) (string, bool) { return "", false })
	if err != nil || !reflect.DeepEqual(headers, map[string]string{"X-Team": "billing"}) {
		t.Errorf("Headers = %v, %v", headers, err)
	}
	if len(c.Vars) != 1 || len(c.Templates) != 1 || len(c.Projects) != 1 || c.Defaults.RetryCount() != 0 {
		t.Errorf("comments leaked into the config: %+v", c)
	}
	// Only the .comment prefix is exempt; other unknown keys still fail.
	if _, err := Parse([]byte(`{"comment": "x", "projects": {}}`)); err == nil {
		t.Error("unknown key \"comment\" (no dot) was accepted")
	}
}

// A profile without groupKey is status-only and valid.
func TestStatusOnlyProfileIsValid(t *testing.T) {
	c := mustParse(t, minimal(`"defaults": {"profile": "jenkins"}, "profiles": {"jenkins": {"healthPath": "/login"}}`, ""))
	if issues := Validate(c); len(issues) != 0 {
		t.Errorf("unexpected issues: %v", issues)
	}
}

func TestUnsetHeaderEnvVarSaysHowToSetIt(t *testing.T) {
	c := mustParse(t, minimal(`"defaults": {"profile": "x"},
		"profiles": {"x": {"healthPath": "/h", "headers": {"Authorization": "Bearer ${env:API_TOKEN}"}}}`, ""))
	_, err := c.Headers("x", func(string) (string, bool) { return "", false })
	if err == nil || !strings.Contains(err.Error(), "environment variable API_TOKEN") || !strings.Contains(err.Error(), "export API_TOKEN=") {
		t.Errorf("err = %v", err)
	}
}

func TestRetriesZeroIsKept(t *testing.T) {
	c := mustParse(t, minimal(`"defaults": {"profile": "flask", "retries": 0}`, ""))
	if c.Defaults.RetryCount() != 0 {
		t.Errorf("retries = %d, want 0", c.Defaults.RetryCount())
	}
}

func TestValidate(t *testing.T) {
	ok := `"defaults": {"profile": "flask"}`
	tests := []struct {
		name     string
		data     string
		wantPath string
		wantMsg  string
	}{
		{"unknown profile", minimal("", `"profile": "nope", "envs": {"dev": "https://a.example.com"}`),
			"$.projects.p.apps.a.profile", `unknown profile "nope"`},
		{"no profile at all", minimal("", ""), "$.projects.p.apps.a.profile", "no profile"},
		{"unknown default profile", minimal(`"defaults": {"profile": "nope"}`, ""), "$.defaults.profile", "unknown profile"},
		{"undefined var in template", minimal(ok+`, "templates": {"jenkins": "${jenkins_host}/${app}"}`, ""),
			"$.templates.jenkins", "undefined variable ${jenkins_host}"},
		{"undefined var in app template", minimal(ok, `"templates": {"swagger": "${nope}"}, "envs": {"dev": "https://a.example.com"}`),
			"$.projects.p.apps.a.templates.swagger", "undefined variable ${nope}"},
		{"env: not allowed in link templates", minimal(ok+`, "templates": {"x": "${env:HOME}"}`, ""),
			"$.templates.x", "undefined variable ${env:HOME}"},
		{"unterminated template", minimal(ok+`, "templates": {"x": "${app"}`, ""), "$.templates.x", "unterminated"},
		{"invalid duration", minimal(`"defaults": {"profile": "flask", "timeout": "5 seconds"}`, ""),
			"$.defaults.timeout", "invalid duration"},
		{"zero duration", minimal(`"defaults": {"profile": "flask", "slowThreshold": "0s"}`, ""),
			"$.defaults.slowThreshold", "greater than zero"},
		{"bad output", minimal(`"defaults": {"profile": "flask", "output": "xml"}`, ""), "$.defaults.output", "unknown output"},
		{"bad concurrency", minimal(`"defaults": {"profile": "flask", "concurrency": -1}`, ""), "$.defaults.concurrency", "1 or more"},
		{"bad url scheme", minimal(ok, `"envs": {"dev": ["https://a.example.com", "ftp://b.example.com"]}`),
			"$.projects.p.apps.a.envs.dev[1]", "scheme must be http or https"},
		{"url without host", minimal(ok, `"envs": {"dev": "payments.dev"}`), "$.projects.p.apps.a.envs.dev[0]", "invalid URL"},
		{"duplicate url", minimal(ok, `"envs": {"dev": ["https://a.example.com", "https://a.example.com/"]}`),
			"$.projects.p.apps.a.envs.dev[1]", "duplicate URL"},
		{"empty env", minimal(ok, `"envs": {"dev": []}`), "$.projects.p.apps.a.envs.dev", "at least one instance"},
		{"no envs", minimal(ok, `"envs": {}`), "$.projects.p.apps.a.envs", "at least one env"},
		{"no projects", `{"projects": {}}`, "$.projects", "at least one project"},
		{"group key without a field", minimal(ok+`, "profiles": {"x": {"healthPath": "/h", "groupKey": "static"}}`, ""),
			"$.profiles.x.groupKey", "at least one response field"},
		{"profile bad health path", minimal(ok+`, "profiles": {"x": {"healthPath": "h", "groupKey": "${v}"}}`, ""),
			"$.profiles.x.healthPath", `must start with "/"`},
		{"header with undefined var", minimal(ok+`, "profiles": {"x": {"healthPath": "/h", "groupKey": "${v}", "headers": {"X-Key": "${nope}"}}}`, ""),
			"$.profiles.x.headers.X-Key", "undefined variable ${nope}"},
		{"var shadows builtin", minimal(ok+`, "vars": {"app": "x"}`, ""), "$.vars.app", "built-in"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := Validate(mustParse(t, tt.data))
			for _, issue := range issues {
				if issue.Path == tt.wantPath && strings.Contains(issue.Message, tt.wantMsg) {
					return
				}
			}
			t.Errorf("want issue at %s containing %q, got %v", tt.wantPath, tt.wantMsg, issues)
		})
	}
}

func TestValidateAllowsEnvRefInHeaders(t *testing.T) {
	c := mustParse(t, minimal(`"defaults": {"profile": "x"}, "vars": {"team": "billing"},
		"profiles": {"x": {"healthPath": "/h", "groupKey": "${v}", "headers": {"Authorization": "Bearer ${env:TOKEN}", "X-Team": "${team}"}}}`, ""))
	if issues := Validate(c); len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
	getenv := func(name string) (string, bool) { return "s3cret", name == "TOKEN" }
	headers, err := c.Headers("x", getenv)
	want := map[string]string{"Authorization": "Bearer s3cret", "X-Team": "billing"}
	if err != nil || !reflect.DeepEqual(headers, want) {
		t.Errorf("Headers = %v, %v; want %v", headers, err, want)
	}
	if _, err := c.Headers("x", func(string) (string, bool) { return "", false }); err == nil {
		t.Error("unset env var: want error")
	}
}

func TestInstancesOrder(t *testing.T) {
	c := mustParse(t, `{
		"defaults": {"profile": "flask"},
		"envOrder": ["dev", "test", "prod"],
		"projects": {
			"zeta": {"apps": {"a": {"profile": "rails", "envs": {"prod": "https://z.example.com:8443/blue/"}}}},
			"alpha": {"apps": {"a": {"envs": {
				"prod": "https://p.example.com", "canary": "https://c.example.com",
				"test": "https://t.example.com", "dev": ["https://d1.example.com", "https://d2.example.com"]}}}}
		}}`)
	var got []string
	for _, inst := range c.Instances() {
		got = append(got, inst.Project+"/"+inst.Env+"/"+inst.Host+"/"+inst.Profile)
	}
	want := []string{
		"alpha/dev/d1.example.com/flask", "alpha/dev/d2.example.com/flask",
		"alpha/test/t.example.com/flask", "alpha/prod/p.example.com/flask",
		"alpha/canary/c.example.com/flask", "zeta/prod/z.example.com:8443/rails",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
	last := c.Instances()[5]
	if last.URL != "https://z.example.com:8443/blue" || last.Label != "z.example.com:8443/blue" {
		t.Errorf("path-based instance: URL %q, Label %q", last.URL, last.Label)
	}
}

func TestLinksOverrideOrder(t *testing.T) {
	const tpl = `{
		"defaults": {"profile": "flask"},
		"vars": {"jenkins_host": "https://jenkins.example.com"},
		"templates": {"swagger": "${base_url}/swagger", "jenkins": "${jenkins_host}/global/${project}/${app}"},
		"projects": {"billing": {
			PROJECT_TEMPLATES
			"apps": {"payments": {
				APP_TEMPLATES
				"envs": {"dev": "https://pay.dev.example.com"}}}}}}`
	tests := []struct {
		name        string
		project     string
		app         string
		wantJenkins string
	}{
		{"global only", "", "", "https://jenkins.example.com/global/billing/payments"},
		{"project overrides global", `"templates": {"jenkins": "${jenkins_host}/team/${app}/${env}"},`, "",
			"https://jenkins.example.com/team/payments/dev"},
		{"app overrides project, literal URL", `"templates": {"jenkins": "${jenkins_host}/team/${app}"},`,
			`"templates": {"jenkins": "https://ci.example.com/pay on ${host}"},`, "https://ci.example.com/pay on pay.dev.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := strings.NewReplacer("PROJECT_TEMPLATES", tt.project, "APP_TEMPLATES", tt.app).Replace(tpl)
			c := mustParse(t, data)
			if issues := Validate(c); len(issues) != 0 {
				t.Fatalf("issues: %v", issues)
			}
			links, err := c.Links(c.Instances()[0])
			if err != nil {
				t.Fatal(err)
			}
			if links["jenkins"] != tt.wantJenkins {
				t.Errorf("jenkins = %q, want %q", links["jenkins"], tt.wantJenkins)
			}
			if links["swagger"] != "https://pay.dev.example.com/swagger" {
				t.Errorf("swagger = %q", links["swagger"])
			}
		})
	}
}

// The shipped example configs must stay valid.
func TestShippedExamplesAreValid(t *testing.T) {
	root := filepath.Join("..", "..")
	files, err := filepath.Glob(filepath.Join(root, "examples", "*.json"))
	if err != nil || len(files) < 3 {
		t.Fatalf("examples not found: %v %v", files, err)
	}
	files = append(files, filepath.Join(root, "envtally.example.json"), filepath.Join(root, "envtally.example.full.json"))
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			if _, err := os.Stat(file); err != nil {
				t.Fatal(err)
			}
			c, err := Load(file)
			if err != nil {
				t.Fatal(err)
			}
			if issues := Validate(c); len(issues) != 0 {
				t.Errorf("issues: %v", issues)
			}
		})
	}
}
