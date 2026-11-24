package cli

import (
	"bytes"
	"encoding/csv"
	"flag"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/LarryBattle/envtally/internal/config"
	"github.com/LarryBattle/envtally/internal/mockserver"
)

var update = flag.Bool("update", false, "rewrite golden files")

// setupMock starts one profile of the mock server's built-in config and
// writes the envtally config that checks every app in it.
func setupMock(t *testing.T, profile string) (configPath string) {
	t.Helper()
	server, err := mockserver.New(mockserver.Default(), profile)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server)
	t.Cleanup(srv.Close)
	t.Setenv("ENVTALLY_HOME", t.TempDir())
	t.Setenv("ENVTALLY_CONFIG", "")
	return writeFile(t, "mock.json", string(server.EnvtallyConfig(srv.URL)))
}

var (
	mockAddr   = regexp.MustCompile(`127\.0\.0\.1:\d+`)
	durations  = regexp.MustCompile(`\b\d+(\.\d+)?(ms|s)\b`)
	jsonMillis = regexp.MustCompile(`("\w+Ms": )\d+`)
)

// normalize replaces what differs between runs: the server's port and the
// response times. The port keeps its width so columns stay aligned.
func normalize(t *testing.T, style, out string) string {
	t.Helper()
	out = mockAddr.ReplaceAllString(out, "127.0.0.1:00000")
	switch style {
	case "json":
		return jsonMillis.ReplaceAllString(out, "${1}0")
	case "csv":
		rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
		if err != nil {
			t.Fatalf("stdout is not CSV: %v\n%s", err, out)
		}
		for col, name := range rows[0] {
			if strings.HasSuffix(name, "_ms") {
				for _, row := range rows[1:] {
					row[col] = "0"
				}
			}
		}
		var buf bytes.Buffer
		w := csv.NewWriter(&buf)
		if err := w.WriteAll(rows); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	return durations.ReplaceAllString(out, "0ms")
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run `go test ./internal/cli -update` to create it)", err)
	}
	// Tolerate CRLF checkouts on Windows.
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("output differs from %s (rerun with -update if intended)\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

// The whole pipeline (config, probe, group, report) against every profile of
// the mock server: all app types deployed to every env.
func TestMockProfilesGolden(t *testing.T) {
	for _, profile := range mockserver.Default().ProfileNames() {
		t.Run(profile, func(t *testing.T) {
			cfg := setupMock(t, profile)
			code, stdout, stderr := run("check", "--config", cfg, "--format", "table")
			wantCode := ExitNotInSync
			if profile == "healthy" || profile == "slow" {
				wantCode = ExitOK // slow is still in sync
			}
			if code != wantCode {
				t.Errorf("exit = %d, want %d\nstderr: %s", code, wantCode, stderr)
			}
			checkGolden(t, "mock_"+profile+"_table", normalize(t, "table", stdout))
		})
	}
}

// The mixed profile in every output style.
func TestMockMixedGoldenStyles(t *testing.T) {
	cfg := setupMock(t, "mixed")
	for _, style := range config.Outputs {
		t.Run(style, func(t *testing.T) {
			code, stdout, stderr := run("check", "--config", cfg, "--format", style)
			if code != ExitNotInSync {
				t.Errorf("exit = %d, want %d\nstderr: %s", code, ExitNotInSync, stderr)
			}
			checkGolden(t, "mock_mixed_"+style, normalize(t, style, stdout))
		})
	}
}

// One env at a time: every app type is there, and the built-in profiles plus
// the config-defined one are announced.
func TestMockOneEnvAcrossAppTypes(t *testing.T) {
	cfg := setupMock(t, "mixed")
	code, stdout, stderr := run("check", "--config", cfg, "--format", "compact", "--include", "^prod$")
	if code != ExitNotInSync {
		t.Errorf("exit = %d, want %d", code, ExitNotInSync)
	}
	wantContains(t, "stdout", stdout,
		"mock/flask-catalog/prod ✗ DRIFT [1.4.1 ×2] [1.4.2 ×1]",
		"mock/node-gateway/prod ✓ IN SYNC [1.4.1|9f8e7d6 ×3]",
		"mock/rails-storefront/prod ✓ IN SYNC [1.4.1|9f8e7d6 ×3]",
		"mock/springboot-ledger/prod ✓ IN SYNC [1.4.1|9f8e7d6 ×3]",
		"mock/springboot-payments/prod ✗ DOWN [", "/springboot-payments.prod.na2.example.com DOWN: HTTP 503]")
	wantContains(t, "stderr", stderr,
		"Group key [flask]: ${version}",
		"Group key [node]: ${version}|${build.sha}",
		"Group key [rails]: ${version}|${revision}",
		"Group key [springboot]: ${build.version}|${git.commit.id}")
}

// --only health skips the info endpoint, so only failing health checks count.
func TestMockOnlyHealth(t *testing.T) {
	cfg := setupMock(t, "outage")
	code, stdout, _ := run("check", "--config", cfg, "--format", "compact", "--only", "health")
	if code != ExitNotInSync {
		t.Errorf("exit = %d, want %d", code, ExitNotInSync)
	}
	wantContains(t, "stdout", stdout,
		"mock/flask-catalog/dev ✓ UP",      // noinfo: health is fine
		"mock/springboot-ledger/test ✓ UP", // partial: health is fine
		"/rails-storefront.test.na1.example.com DOWN: HTTP 500]",
		"/springboot-payments.prod.na2.example.com DOWN: HTTP 503]")
}
