package report

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LarryBattle/envtally/internal/config"
	"github.com/LarryBattle/envtally/internal/group"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fixture: one in-sync env, one drifted, one down (with a slow instance), one unknown.
func fixture() group.Result {
	inst := func(app, env, host, state, key, detail string, health, info time.Duration) group.Instance {
		return group.Instance{
			Project: "billing", App: app, Env: env, URL: "https://" + host, Host: host, Profile: "springboot",
			State: state, Key: key, KeyComplete: key != "" && !strings.Contains(key, "?"), Detail: detail,
			Health: health, Info: info, Total: health + info, Slow: health+info > time.Second,
		}
	}
	ms := time.Millisecond
	instances := []group.Instance{
		inst("payments", "dev", "payments.dev.na1.host.com", group.InstUp, "1.4.2|a1b2", "", 60*ms, 50*ms),
		inst("payments", "dev", "payments.dev.na2.host.com", group.InstUp, "1.4.1|9f8e", "", 50*ms, 45*ms),
		inst("payments", "prod", "payments.na1.host.com", group.InstUp, "1.4.1|9f8e", "", 40*ms, 58*ms),
		inst("payments", "prod", "payments.na2.host.com", group.InstUp, "1.4.1|9f8e", "", 700*ms, 500*ms),
		inst("ledger", "dev", "ledger.dev.na1.host.com", group.InstUp, "2.0.0|77aa", "", 30*ms, 31*ms),
		inst("ledger", "dev", "ledger.dev.na2.host.com", group.InstDown, "", "HTTP 503", 12*ms, 0),
		inst("ledger", "prod", "ledger.host.com", group.InstUp, "2.0.0|?", "", 20*ms, 22*ms),
	}
	return group.Result{
		Envs:         group.Evaluate(instances, ""),
		KeyTemplates: map[string]string{"springboot": config.BuiltinProfiles()["springboot"].GroupKey},
	}
}

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run `go test ./internal/report -update` to create it)", err)
	}
	// Tolerate CRLF checkouts on Windows.
	if string(got) != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("output differs from %s (rerun with -update if intended)\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

func TestRenderGolden(t *testing.T) {
	for _, style := range config.Outputs {
		t.Run(style, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Render(&buf, style, fixture(), Options{}); err != nil {
				t.Fatal(err)
			}
			checkGolden(t, style, buf.Bytes())
		})
	}
}

func TestRenderUnknownStyle(t *testing.T) {
	if err := Render(&bytes.Buffer{}, "xml", fixture(), Options{}); err == nil {
		t.Fatal("want error")
	}
}

func TestMachineOutputsParse(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, "json", fixture(), Options{}); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		OK                bool
		GroupKeyTemplates map[string]string
		Envs              []struct {
			Status    string
			Instances []struct{ TotalMs int64 }
		}
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OK || len(doc.Envs) != 4 || doc.GroupKeyTemplates["springboot"] == "" || doc.Envs[0].Instances[0].TotalMs != 110 {
		t.Errorf("unexpected json: %+v", doc)
	}

	buf.Reset()
	if err := Render(&buf, "csv", fixture(), Options{}); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil || len(rows) != 8 {
		t.Errorf("csv: %d rows, err %v; want header + 7", len(rows), err)
	}
}

func TestColor(t *testing.T) {
	var plainOut, colored bytes.Buffer
	_ = Render(&plainOut, "table", fixture(), Options{})
	_ = Render(&colored, "table", fixture(), Options{Color: true})
	if strings.Contains(plainOut.String(), "\x1b[") {
		t.Error("escape codes present with color off")
	}
	if !strings.Contains(colored.String(), "\x1b[31m✗ DOWN\x1b[0m") {
		t.Error("DOWN is not red with color on")
	}
	// Stripping the codes must give the plain output: color must not change alignment.
	stripped := colored.String()
	for _, code := range []string{"\x1b[0m", "\x1b[1m", "\x1b[31m", "\x1b[32m", "\x1b[33m"} {
		stripped = strings.ReplaceAll(stripped, code, "")
	}
	if stripped != plainOut.String() {
		t.Error("colored output is not aligned like plain output")
	}
}

func TestDur(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "0ms"},
		{98 * time.Millisecond, "98ms"},
		{98600 * time.Microsecond, "99ms"},
		{1200 * time.Millisecond, "1.2s"},
		{59 * time.Second, "59.0s"},
		{65 * time.Second, "1m5s"},
	}
	for _, tt := range tests {
		if got := Dur(tt.in); got != tt.want {
			t.Errorf("Dur(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRenderURLsGolden(t *testing.T) {
	rows := []URLRow{
		{"billing", "payments", "dev", "https://payments.dev.na1.host.com", map[string]string{
			"swagger": "https://payments.dev.na1.host.com/swagger-ui/index.html", "jenkins": "https://jenkins.example.com/job/billing/job/payments/"}},
		{"billing", "ledger", "prod", "https://ledger.host.com", map[string]string{"jenkins": "https://jenkins.example.com/job/billing/job/ledger/"}},
	}
	for _, style := range []string{"table", "markdown", "csv", "json"} {
		t.Run(style, func(t *testing.T) {
			var buf bytes.Buffer
			if err := RenderURLs(&buf, style, rows, Options{}); err != nil {
				t.Fatal(err)
			}
			checkGolden(t, "urls_"+style, buf.Bytes())
		})
	}
}
