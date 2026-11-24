package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
)

// fleet is a fake set of Spring Boot instances behind one httptest server.
// An instance's base URL is <server>/<name>.
type fleet struct {
	mu        sync.Mutex
	instances map[string]fakeInstance
	infoHits  map[string]int
}

type fakeInstance struct {
	healthStatus int
	version      string
	laterVersion string // if set, served by every info call after the first
}

func (f *fleet) set(name string, inst fakeInstance) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.instances[name] = inst
}

func (f *fleet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	f.mu.Lock()
	inst, ok := f.instances[parts[0]]
	f.mu.Unlock()
	if !ok || len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	switch parts[1] {
	case "actuator/health":
		status := "UP"
		if inst.healthStatus != http.StatusOK {
			status = "DOWN"
		}
		w.WriteHeader(inst.healthStatus)
		_, _ = fmt.Fprintf(w, `{"status":%q}`, status)
	case "actuator/info":
		version := inst.version
		f.mu.Lock()
		f.infoHits[parts[0]]++
		if inst.laterVersion != "" && f.infoHits[parts[0]] > 1 {
			version = inst.laterVersion
		}
		f.mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"build":{"version":%q},"git":{"commit":{"id":"c0ffee"}}}`, version)
	default:
		http.NotFound(w, r)
	}
}

// setup starts the fleet (one in-sync app, one drifted, one down) and writes
// a config for it. ENVTALLY_HOME points at a temp dir, so nothing is remembered
// outside the test.
func setup(t *testing.T) (f *fleet, configPath string) {
	t.Helper()
	f = &fleet{infoHits: map[string]int{}, instances: map[string]fakeInstance{
		"sync1": {200, "1.0.0", ""}, "sync2": {200, "1.0.0", ""},
		"drift1": {200, "2.0.0", ""}, "drift2": {200, "2.1.0", ""},
		"down1": {200, "3.0.0", ""}, "down2": {503, "3.0.0", ""},
	}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("ENVTALLY_HOME", t.TempDir())
	t.Setenv("ENVTALLY_CONFIG", "")

	app := func(name string) string {
		return fmt.Sprintf(`%q: {"envs": {"dev": ["%s/%s1", "%s/%s2"]}}`, "app-"+name, srv.URL, name, srv.URL, name)
	}
	cfg := `{
		"defaults": {"profile": "springboot", "output": "compact"},
		"templates": {"swagger": "${base_url}/swagger-ui/index.html"},
		"projects": {"shop": {"apps": {` + app("sync") + `,` + app("drift") + `,` + app("down") + `}}}}`
	return f, writeFile(t, "config.json", cfg)
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func run(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = Execute(context.Background(), args, &out, &errOut, BuildInfo{Version: "1.2.3", Commit: "abc", Date: "today"})
	return code, out.String(), errOut.String()
}

func wantContains(t *testing.T, label, text string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Errorf("%s lacks %q:\n%s", label, part, text)
		}
	}
}

func TestCheckShowsSyncDriftAndDown(t *testing.T) {
	_, cfg := setup(t)
	code, stdout, stderr := run("check", "--config", cfg)
	if code != ExitNotInSync {
		t.Errorf("exit = %d, want %d", code, ExitNotInSync)
	}
	wantContains(t, "stdout", stdout,
		"shop/app-sync/dev ✓ IN SYNC [1.0.0|c0ffee ×2]",
		"shop/app-drift/dev ✗ DRIFT [2.0.0|c0ffee ×1] [2.1.0|c0ffee ×1]",
		"shop/app-down/dev ✗ DOWN [", "/down2 DOWN: HTTP 503]")
	// Diagnostics come first and go to stderr.
	wantContains(t, "stderr", stderr, "Using config: "+cfg+" (from --config)", "Group key [springboot]: ${build.version}|${git.commit.id}")
}

func TestCheckJSONKeepsStdoutClean(t *testing.T) {
	_, cfg := setup(t)
	code, stdout, _ := run("check", "--config", cfg, "--format", "json", "-vv")
	if code != ExitNotInSync {
		t.Errorf("exit = %d", code)
	}
	var doc struct {
		OK   bool `json:"ok"`
		Envs []struct {
			App, Status string
		} `json:"envs"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not pure JSON: %v\n%s", err, stdout)
	}
	got := map[string]string{}
	for _, e := range doc.Envs {
		got[e.App] = e.Status
	}
	want := map[string]string{"app-sync": "IN SYNC", "app-drift": "DRIFT", "app-down": "DOWN"}
	if fmt.Sprint(got) != fmt.Sprint(want) || doc.OK {
		t.Errorf("got %v ok=%v, want %v ok=false", got, doc.OK, want)
	}
}

func TestExitCodesAndFilters(t *testing.T) {
	_, cfg := setup(t)
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string // substring of stderr
	}{
		{"filtered to the in-sync app", []string{"check", "--include", "app=app-sync"}, ExitOK, ""},
		{"exclude leaves only in-sync", []string{"check", "--exclude", "drift", "--exclude", "app=app-down"}, ExitOK, ""},
		{"filter fields combine", []string{"check", "--include", "app=app-(sync|drift)", "--include", "env=dev", "--exclude", "url=/drift[12]$"}, ExitOK, ""},
		{"unknown filter field", []string{"check", "--include", "evn=dev"}, ExitUsage, "unknown --include field"},
		{"empty filter", []string{"check", "--include", ""}, ExitUsage, "empty --include pattern"},
		{"drift", []string{"check", "--include", "^app-drift$"}, ExitNotInSync, ""},
		{"down", []string{"check", "--include", "down"}, ExitNotInSync, ""},
		{"only health ignores drift", []string{"check", "--include", "drift", "--only", "health"}, ExitOK, "not evaluated"},
		{"only info still sees drift", []string{"check", "--include", "drift", "--only", "info"}, ExitNotInSync, ""},
		{"invalid regex", []string{"check", "--include", "("}, ExitUsage, "invalid --include regex"},
		{"filters match nothing", []string{"check", "--include", "nomatch"}, ExitUsage, "no instances match"},
		{"bad --only", []string{"check", "--only", "both"}, ExitUsage, "unknown --only"},
		{"bad --format", []string{"check", "--format", "xml"}, ExitUsage, "unknown --format"},
		{"unknown flag", []string{"check", "--nope"}, ExitUsage, "unknown flag"},
		{"unknown command", []string{"frobnicate"}, ExitUsage, "unknown command"},
		{"zero watch interval", []string{"check", "--watch", "0s"}, ExitUsage, "greater than zero"},
		{"missing config file", []string{"check", "--config", filepath.Join(t.TempDir(), "nope.json")}, ExitUsage, "Error:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.args
			if !strings.Contains(strings.Join(args, " "), "--config") && args[0] == "check" {
				args = append(args, "--config", cfg)
			}
			code, _, stderr := run(args...)
			if code != tt.wantCode {
				t.Errorf("exit = %d, want %d\nstderr: %s", code, tt.wantCode, stderr)
			}
			wantContains(t, "stderr", stderr, tt.wantErr)
		})
	}
}

func TestConfigIsRememberedAndRequired(t *testing.T) {
	_, cfg := setup(t)

	code, _, stderr := run("check")
	if code != ExitUsage || !strings.Contains(stderr, "No config file. Run `envtally init` or pass `--config <path>`.") {
		t.Fatalf("no config: exit %d, stderr %q", code, stderr)
	}

	// A config that fails to load is not remembered.
	bad := writeFile(t, "bad.json", `{"projects": {}}`)
	if code, _, _ := run("config", "validate", "--config", bad); code != ExitUsage {
		t.Fatalf("bad config: exit %d", code)
	}
	if code, _, _ := run("config", "path"); code != ExitUsage {
		t.Fatalf("bad config was remembered")
	}

	if code, _, _ := run("check", "--config", cfg, "--include", "app-sync"); code != ExitOK {
		t.Fatalf("first run: exit %d", code)
	}
	code, _, stderr = run("check", "--include", "app-sync")
	if code != ExitOK || !strings.Contains(stderr, "Using config: "+cfg+" (from remembered)") {
		t.Errorf("second run: exit %d, stderr %q", code, stderr)
	}
	code, stdout, stderr := run("config", "path")
	if code != ExitOK || stdout != cfg+"\n" || !strings.Contains(stderr, "(from remembered)") {
		t.Errorf("config path: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}

	// ENVTALLY_CONFIG beats the remembered path and does not replace it.
	t.Setenv("ENVTALLY_CONFIG", bad)
	if _, stdout, stderr := run("config", "path"); stdout != bad+"\n" || !strings.Contains(stderr, "(from ENVTALLY_CONFIG)") {
		t.Errorf("env var not used: %q %q", stdout, stderr)
	}
}

func TestConfigValidate(t *testing.T) {
	setup(t)
	bad := writeFile(t, "bad.json", `{
		"templates": {"jenkins": "${jenkins_host}/job/${app}"},
		"defaults": {"timeout": "soon"},
		"projects": {"p": {"apps": {"a": {"profile": "cobol", "envs": {"dev": "not a url"}}}}}}`)
	code, stdout, stderr := run("config", "validate", "--config", bad)
	if code != ExitUsage || stdout != "" {
		t.Errorf("exit = %d, stdout = %q", code, stdout)
	}
	wantContains(t, "stderr", stderr,
		"Config has 4 problem(s):",
		"$.templates.jenkins: undefined variable ${jenkins_host}",
		`$.projects.p.apps.a.profile: unknown profile "cobol"`,
		"$.defaults.timeout: invalid duration",
		"$.projects.p.apps.a.envs.dev[0]: invalid URL")

	_, good := setup(t)
	code, stdout, _ = run("config", "validate", "--config", good)
	if code != ExitOK || !strings.Contains(stdout, "Config OK: 1 project(s), 6 instance(s)") {
		t.Errorf("good config: exit %d, stdout %q", code, stdout)
	}
}

func TestConfigShowAndSchema(t *testing.T) {
	_, cfg := setup(t)
	code, stdout, _ := run("config", "show", "--config", cfg)
	var shown struct {
		Defaults struct{ Timeout string }
		Profiles map[string]json.RawMessage
		Projects map[string]struct {
			Apps map[string]struct{ Envs map[string][]string }
		}
	}
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil || code != ExitOK {
		t.Fatalf("exit %d, err %v", code, err)
	}
	if shown.Defaults.Timeout != "5s" || len(shown.Profiles) != 3 || len(shown.Projects["shop"].Apps["app-sync"].Envs["dev"]) != 2 {
		t.Errorf("config show is not normalized: %s", stdout)
	}

	code, stdout, _ = run("config", "schema")
	if code != ExitOK || !json.Valid([]byte(stdout)) || !strings.Contains(stdout, `"healthPath"`) {
		t.Errorf("config schema: exit %d", code)
	}
}

func TestURLs(t *testing.T) {
	_, cfg := setup(t)
	code, stdout, _ := run("urls", "--config", cfg, "--include", "sync", "--format", "csv")
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if code != ExitOK || len(lines) != 3 || lines[0] != "project,app,env,url,swagger" || !strings.HasSuffix(lines[1], "/sync1/swagger-ui/index.html") {
		t.Errorf("exit %d, stdout:\n%s", code, stdout)
	}
}

func TestInit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVTALLY_HOME", home)
	t.Setenv("ENVTALLY_CONFIG", "")
	path := filepath.Join(home, "config.json")

	if code, stdout, _ := run("init"); code != ExitOK || !strings.Contains(stdout, path) {
		t.Fatalf("init: exit %d, stdout %q", code, stdout)
	}
	if _, err := os.Stat(filepath.Join(home, "envtally.schema.json")); err != nil {
		t.Errorf("schema not written next to the config: %v", err)
	}
	// The sample is valid and is now the remembered config.
	if code, _, stderr := run("config", "validate"); code != ExitOK || !strings.Contains(stderr, path+" (from remembered)") {
		t.Errorf("validate after init: exit %d, stderr %q", code, stderr)
	}
	if err := os.WriteFile(path, []byte("my edits"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := run("init"); code != ExitUsage || !strings.Contains(stderr, "--force") {
		t.Errorf("init over existing file: exit %d, stderr %q", code, stderr)
	}
	if data, _ := os.ReadFile(path); string(data) != "my edits" {
		t.Error("init overwrote the config without --force")
	}
	if code, _, _ := run("init", "--force"); code != ExitOK {
		t.Errorf("init --force: exit %d", code)
	}
}

func TestInitExamples(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVTALLY_HOME", home)
	t.Setenv("ENVTALLY_CONFIG", "")
	path := filepath.Join(home, "config.json")

	// --print shows the example and writes nothing.
	code, stdout, _ := run("init", "--example", "full", "--print")
	if code != ExitOK || !strings.Contains(stdout, `"Authorization": "Bearer ${env:INTERNAL_API_TOKEN}"`) {
		t.Errorf("init --print: exit %d, stdout:\n%s", code, stdout)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("init --print wrote the config")
	}
	if code, _, stderr := run("init", "--example", "nope"); code != ExitUsage || !strings.Contains(stderr, "basic or full") {
		t.Errorf("unknown example: exit %d, stderr %q", code, stderr)
	}

	for _, example := range []string{"basic", "full"} {
		if code, stdout, _ := run("init", "--force", "--example", example); code != ExitOK || !strings.Contains(stdout, example+" example") {
			t.Fatalf("init --example %s: exit %d, stdout %q", example, code, stdout)
		}
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), `".comment"`) {
			t.Errorf("%s example has no .comment notes", example)
		}
		if code, _, stderr := run("config", "validate"); code != ExitOK {
			t.Errorf("%s example does not validate: %s", example, stderr)
		}
	}
	// The comments are notes only: they never reach the normalized config.
	if _, stdout, _ := run("config", "show"); strings.Contains(stdout, ".comment") {
		t.Errorf("config show leaked comments:\n%s", stdout)
	}
}

// A profile without groupKey only needs HTTP 2xx: no JSON, no version compare.
func TestCheckStatusOnlyProfile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/up/login" {
			http.Error(w, "nope", http.StatusForbidden)
			return
		}
		_, _ = fmt.Fprint(w, "<html>Sign in</html>")
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ENVTALLY_HOME", t.TempDir())
	t.Setenv("ENVTALLY_CONFIG", "")
	config := func(base string) string {
		return writeFile(t, "config.json", fmt.Sprintf(`{
			"defaults": {"profile": "jenkins", "output": "compact"},
			"profiles": {"jenkins": {".comment": "status only", "healthPath": "/login"}},
			"projects": {"tools": {"apps": {"jenkins": {"envs": {"prod": "%s/%s"}}}}}}`, srv.URL, base))
	}

	code, stdout, stderr := run("check", "--config", config("up"))
	if code != ExitOK {
		t.Errorf("exit = %d, want %d\n%s%s", code, ExitOK, stdout, stderr)
	}
	wantContains(t, "stdout", stdout, "tools/jenkins/prod ✓ UP")
	wantContains(t, "stderr", stderr, "Group key [jenkins]: none (status only")

	code, stdout, _ = run("check", "--config", config("down"))
	if code != ExitNotInSync {
		t.Errorf("exit = %d, want %d", code, ExitNotInSync)
	}
	wantContains(t, "stdout", stdout, "tools/jenkins/prod ✗ DOWN", "HTTP 403")
}

func TestVersionNeedsNoConfig(t *testing.T) {
	t.Setenv("ENVTALLY_HOME", t.TempDir())
	code, stdout, stderr := run("version")
	if code != ExitOK || !strings.HasPrefix(stdout, "envtally 1.2.3 (commit abc, built today") || stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestWatchUntilInSync(t *testing.T) {
	f, cfg := setup(t)
	// drift2 reports 2.1.0 on the first check and 2.0.0 (deployed) afterwards.
	f.set("drift2", fakeInstance{healthStatus: 200, version: "2.1.0", laterVersion: "2.0.0"})

	code, stdout, stderr := run("check", "--config", cfg, "--include", "drift", "--watch", "10ms", "--watch-timeout", "30s")
	if code != ExitOK {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	wantContains(t, "stdout", stdout,
		"shop/app-drift/dev ✗ DRIFT",       // first snapshot
		"key 2.1.0|c0ffee -> 2.0.0|c0ffee", // then only the changes
		"shop/app-drift/dev: DRIFT -> IN SYNC",
		"✓ in sync after 0s (2 checks)")
	wantContains(t, "stderr", stderr, "\a")
}

func TestWatchTimeout(t *testing.T) {
	_, cfg := setup(t)
	code, stdout, stderr := run("check", "--config", cfg, "--include", "down", "--format", "json",
		"--watch", "10ms", "--watch-timeout", "60ms")
	if code != ExitNotInSync {
		t.Errorf("exit = %d, want %d", code, ExitNotInSync)
	}
	// json: exactly one document on stdout, progress on stderr.
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Errorf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	wantContains(t, "stderr", stderr, "timed out after")
}

// Help shows short examples, and the root help ends with common tasks.
func TestHelpHasExamples(t *testing.T) {
	code, stdout, _ := run("--help")
	if code != ExitOK {
		t.Errorf("exit = %d", code)
	}
	wantContains(t, "root help", stdout, "Examples:", "-f, --format string", "-c, --config string", "Common tasks:", "envtally check -i env=prod")
	if !strings.HasSuffix(strings.TrimSpace(stdout), "# print URLs and links, no network calls") {
		t.Errorf("the common tasks are not at the bottom:\n%s", stdout)
	}

	_, stdout, _ = run("check", "--help")
	wantContains(t, "check help", stdout, "Examples:", "-i, --include PATTERN", "FIELD project|app|env|host|url", "-x, --exclude PATTERN", "-w, --watch", "More:")

	// Every command has examples.
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Example == "" && cmd.Name() != "help" && cmd.Name() != "completion" && cmd.Parent().Name() != "completion" {
			t.Errorf("%q has no examples", cmd.CommandPath())
		}
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	root := (&runner{}).rootCmd()
	root.InitDefaultCompletionCmd()
	for _, sub := range root.Commands() {
		walk(sub)
	}
}

// --output was renamed to --format.
func TestOutputFlagIsGone(t *testing.T) {
	_, cfg := setup(t)
	code, _, stderr := run("check", "--config", cfg, "--output", "json")
	if code != ExitUsage || !strings.Contains(stderr, "unknown flag: --output") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	if code, stdout, _ := run("check", "-c", cfg, "-f", "compact", "-i", "app-sync"); code != ExitOK || !strings.Contains(stdout, "IN SYNC") {
		t.Errorf("short flags: exit %d, stdout %q", code, stdout)
	}
}
