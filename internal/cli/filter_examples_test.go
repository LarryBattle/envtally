package cli

import (
	"encoding/csv"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// exampleFleet is the fleet the documented filter examples are written for:
// two regions (na1, na2), a preprod env, an app whose name contains "prod",
// and an app behind a gateway path. Keys are short ids used in the tests.
var exampleFleet = map[string]struct{ project, app, env, url string }{
	"pay-dev-na1":      {"billing", "payments", "dev", "https://payments.dev.na1.host.com"},
	"pay-dev-na2":      {"billing", "payments", "dev", "https://payments.dev.na2.host.com"},
	"pay-prod-na1":     {"billing", "payments", "prod", "https://payments.na1.host.com"},
	"pay-prod-na2":     {"billing", "payments", "prod", "https://payments.na2.host.com"},
	"ledger-dev":       {"billing", "ledger", "dev", "https://ledger.dev.na1.host.com"},
	"ledger-prod":      {"billing", "ledger", "prod", "https://ledger.na1.host.com"},
	"products-dev":     {"shop", "products", "dev", "https://products.dev.na1.host.com"},
	"products-preprod": {"shop", "products", "preprod", "https://products.preprod.na1.host.com"},
	"products-prod":    {"shop", "products", "prod", "https://products.na1.host.com"},
	"reports-dev":      {"shop", "reports", "dev", "http://gateway.dev.host.com/reports/v1"},
	"reports-prod":     {"shop", "reports", "prod", "https://gateway.host.com/reports/v2"},
}

const exampleFleetConfig = `{
	"defaults": {"profile": "springboot"},
	"envOrder": ["dev", "preprod", "prod"],
	"projects": {
		"billing": {"apps": {
			"payments": {"envs": {
				"dev": ["https://payments.dev.na1.host.com", "https://payments.dev.na2.host.com"],
				"prod": ["https://payments.na1.host.com", "https://payments.na2.host.com"]}},
			"ledger": {"envs": {
				"dev": "https://ledger.dev.na1.host.com",
				"prod": "https://ledger.na1.host.com"}}}},
		"shop": {"apps": {
			"products": {"envs": {
				"dev": "https://products.dev.na1.host.com",
				"preprod": "https://products.preprod.na1.host.com",
				"prod": "https://products.na1.host.com"}},
			"reports": {"envs": {
				"dev": "http://gateway.dev.host.com/reports/v1",
				"prod": "https://gateway.host.com/reports/v2"}}}}}}`

// Groups of ids, to keep the expectations below readable.
const (
	payDev   = "pay-dev-na1 pay-dev-na2"
	payProd  = "pay-prod-na1 pay-prod-na2"
	allProd  = payProd + " ledger-prod products-prod reports-prod"
	notInDev = allProd + " products-preprod"
)

// filterExamples maps the filter flags of every documented example (README
// and --help) to the instances it must select. Keys are normalized: short
// flags, single spaces, no quotes.
var filterExamples = map[string]string{
	// Everyday filters.
	"-i env=prod":        allProd,
	"-i app=payments":    payDev + " " + payProd,
	"-i project=billing": payDev + " " + payProd + " ledger-dev ledger-prod",
	"-x env=dev":         notInDev,

	// Combining filters.
	"-i app=payments -i env=prod":               payProd,
	"-i app=payments -i app=ledger -i env=prod": payProd + " ledger-prod",
	"-i app=payments|ledger -i env=prod":        payProd + " ledger-prod",
	"-i project=billing -x env=dev":             payProd + " ledger-prod",
	"-i app=payments -x env=dev":                payProd,
	"-i env=dev -i env=preprod":                 payDev + " ledger-dev products-dev products-preprod reports-dev",
	"-i env=prod -x app=reports -x app=ledger":  payProd + " products-prod",
	"-x env=dev|test":                           notInDev,

	// Regex, hosts and URLs.
	"-i app=pay.*":                payDev + " " + payProd,
	"-i env=.*prod":               notInDev,
	`-i env=prod -i host=\.na2\.`: "pay-prod-na2",
	`-i env=prod -x host=\.na2\.`: "pay-prod-na1 ledger-prod products-prod reports-prod",
	"-i url=/reports/":            "reports-dev reports-prod",
	"-i url=^http://":             "reports-dev",
	"-i app=(?i)PAYMENTS":         payDev + " " + payProd,

	// Plain patterns: anywhere in any field, so "prod" also finds the
	// products app and the preprod env.
	"-i payments":             payDev + " " + payProd,
	"-i prod":                 notInDev + " products-dev",
	"-i ^prod$":               allProd,
	"-i payments -i ledger":   payDev + " " + payProd + " ledger-dev ledger-prod",
	"-i payments -i env=prod": payProd,
}

// setupExampleFleet writes the config for exampleFleet. Nothing is probed.
func setupExampleFleet(t *testing.T) (configPath string) {
	t.Helper()
	t.Setenv("ENVTALLY_HOME", t.TempDir())
	t.Setenv("ENVTALLY_CONFIG", "")
	return writeFile(t, "fleet.json", exampleFleetConfig)
}

// selected runs `urls` with the filter flags and returns the sorted ids of
// the instances it printed.
func selected(t *testing.T, configPath string, filterArgs []string) (ids []string, code int, stderr string) {
	t.Helper()
	args := append([]string{"urls", "--config", configPath, "--format", "csv"}, filterArgs...)
	code, stdout, stderr := run(args...)
	if code != ExitOK {
		return nil, code, stderr
	}
	rows, err := csv.NewReader(strings.NewReader(stdout)).ReadAll()
	if err != nil {
		t.Fatalf("stdout is not CSV: %v\n%s", err, stdout)
	}
	for _, row := range rows[1:] { // project,app,env,url
		id := ""
		for name, inst := range exampleFleet {
			if inst.project == row[0] && inst.app == row[1] && inst.env == row[2] && inst.url == row[3] {
				id = name
			}
		}
		if id == "" {
			t.Fatalf("urls printed an instance that is not in exampleFleet: %v", row)
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, code, stderr
}

// Every documented example selects exactly the instances it says it does.
func TestFilterExamples(t *testing.T) {
	cfg := setupExampleFleet(t)
	if all, _, stderr := selected(t, cfg, nil); len(all) != len(exampleFleet) {
		t.Fatalf("the config has %d instances, exampleFleet has %d\n%s", len(all), len(exampleFleet), stderr)
	}
	for flags, want := range filterExamples {
		t.Run(flags, func(t *testing.T) {
			got, code, stderr := selected(t, cfg, strings.Fields(flags))
			if code != ExitOK {
				t.Fatalf("exit = %d\n%s", code, stderr)
			}
			wantIDs := strings.Fields(want)
			sort.Strings(wantIDs)
			if strings.Join(got, " ") != strings.Join(wantIDs, " ") {
				t.Errorf("selected:\n  %v\nwant:\n  %v", got, wantIDs)
			}
		})
	}
}

// Mistakes stop the run with exit code 2 instead of checking the wrong set.
func TestFilterMistakes(t *testing.T) {
	cfg := setupExampleFleet(t)
	tests := []struct {
		name    string
		flags   []string
		wantErr string
	}{
		{"typo in a name", []string{"-i", "env=prd"}, "no instances match the filters (11 in config)"},
		{"part of a name", []string{"-i", "app=pay"}, "match the whole name"},
		{"fields that never meet", []string{"-i", "app=ledger", "-i", "env=preprod"}, "no instances match"},
		{"everything excluded", []string{"-x", "host=host"}, "no instances match"},
		{"typo in a field", []string{"-x", "evn=dev"}, `unknown --exclude field "evn"`},
		{"unset variable", []string{"-i", "env="}, "empty --include pattern"},
		{"empty pattern", []string{"-i", ""}, "empty --include pattern"},
		{"invalid regex", []string{"-i", "app=("}, `invalid --include regex "app=("`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, code, stderr := selected(t, cfg, tt.flags)
			if code != ExitUsage {
				t.Errorf("exit = %d, want %d", code, ExitUsage)
			}
			wantContains(t, "stderr", stderr, tt.wantErr)
		})
	}
}

// exampleLine is an envtally command in the README or in --help.
var exampleLine = regexp.MustCompile(`^(\$ )?envtally (check|urls)\b`)

// documentedFilters returns the normalized filter flags of each example
// command in text that uses --include or --exclude.
func documentedFilters(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !exampleLine.MatchString(line) {
			continue
		}
		var flags []string
		words := shellWords(line)
		for i := 0; i+1 < len(words); i++ {
			switch words[i] {
			case "-i", "--include":
				flags = append(flags, "-i", words[i+1])
			case "-x", "--exclude":
				flags = append(flags, "-x", words[i+1])
			}
		}
		if len(flags) > 0 {
			out = append(out, strings.Join(flags, " "))
		}
	}
	return out
}

// shellWords splits a command line the way sh would for these examples:
// quotes group, and a comment, pipe or redirect ends the command.
func shellWords(line string) []string {
	var words []string
	var word strings.Builder
	var quote rune
	started := false
	flush := func() {
		if started {
			words = append(words, word.String())
			word.Reset()
			started = false
		}
	}
	for _, c := range line {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, started = c, true
		case c == ' ' || c == '\t':
			flush()
		case (c == '#' && !started) || c == '|' || c == '>':
			flush()
			return words
		default:
			word.WriteRune(c)
			started = true
		}
	}
	flush()
	return words
}

// Every filter example in the README and in --help has a case in
// filterExamples, so the docs cannot drift from the tested behavior.
func TestDocumentedFilterExamplesAreTested(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{"README.md": string(readme)}
	for _, args := range [][]string{{"--help"}, {"check", "--help"}, {"urls", "--help"}} {
		_, stdout, _ := run(args...)
		sources["envtally "+strings.Join(args, " ")] = stdout
	}
	documented := map[string]bool{}
	for source, text := range sources {
		found := documentedFilters(text)
		if len(found) == 0 {
			t.Errorf("%s: no filter examples found", source)
		}
		for _, flags := range found {
			documented[flags] = true
			if _, ok := filterExamples[flags]; !ok {
				t.Errorf("%s: example %q has no case in filterExamples", source, flags)
			}
		}
	}
	for flags := range filterExamples {
		if !documented[flags] {
			t.Errorf("filterExamples case %q is not in the README or --help; remove it or document it", flags)
		}
	}
}
