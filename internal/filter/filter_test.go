package filter

import (
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	instance := Fields{
		Project: "billing", App: "payments", Env: "dev",
		Host: "payments.dev.na1.host.com", URL: "https://payments.dev.na1.host.com/api/v2",
	}
	tests := []struct {
		name     string
		includes []string
		excludes []string
		want     bool
	}{
		{"no filters keeps everything", nil, nil, true},

		// Plain REGEX: anywhere in any field.
		{"plain matches app", []string{"^pay"}, nil, true},
		{"plain matches env exactly", []string{"^dev$"}, nil, true},
		{"plain matches host", []string{`na1\.host`}, nil, true},
		{"plain matches URL scheme", []string{"^https://"}, nil, true},
		{"plain matches URL path", []string{"/api/v2$"}, nil, true},
		{"plain matches nothing", []string{"^prod$"}, nil, false},
		{"plain patterns are alternatives", []string{"^prod$", "billing"}, nil, true},
		{"case-insensitive flag", []string{"(?i)PAYMENTS"}, nil, true},

		// FIELD=REGEX: that field only.
		{"project", []string{"project=billing"}, nil, true},
		{"app", []string{"app=payments"}, nil, true},
		{"env", []string{"env=dev"}, nil, true},
		{"host", []string{`host=\.na1\.`}, nil, true},
		{"url", []string{"url=/api/"}, nil, true},
		{"field name is case-insensitive", []string{"ENV=dev"}, nil, true},
		{"env does not look at the host", []string{"env=na1"}, nil, false},
		{"app does not look at the project", []string{"app=billing"}, nil, false},
		{"host does not look at the path", []string{"host=/api/"}, nil, false},

		// Names match whole; host and url match anywhere.
		{"name must match whole", []string{"app=pay"}, nil, false},
		{"name with wildcard", []string{"app=pay.*"}, nil, true},
		{"name alternation is whole too", []string{"env=de|v"}, nil, false},
		{"name alternation", []string{"env=dev|test"}, nil, true},
		{"name with case-insensitive flag", []string{"env=(?i)DEV"}, nil, true},
		{"host matches anywhere", []string{"host=dev"}, nil, true},
		{"host can be anchored", []string{"host=^dev"}, nil, false},

		// Same field: alternatives. Different fields: all must match.
		{"same field, one matches", []string{"env=prod", "env=dev"}, nil, true},
		{"different fields, both match", []string{"app=payments", "env=dev"}, nil, true},
		{"different fields, one misses", []string{"app=payments", "env=prod"}, nil, false},
		{"plain and field, both match", []string{"na1", "env=dev"}, nil, true},
		{"plain and field, field misses", []string{"na1", "env=prod"}, nil, false},
		{"plain and field, plain misses", []string{"na2", "env=dev"}, nil, false},

		// Any exclude drops the instance.
		{"exclude wins over include", []string{"billing"}, []string{"na1"}, false},
		{"exclude only", nil, []string{"^dev$"}, false},
		{"exclude by field", nil, []string{"env=dev"}, false},
		{"exclude by field that misses", nil, []string{"env=de"}, true},
		{"any exclude is enough", nil, []string{"env=prod", `host=\.na1\.`}, false},
		{"exclude that misses", nil, []string{"^prod$"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := New(tt.includes, tt.excludes)
			if err != nil {
				t.Fatal(err)
			}
			if got := f.Match(instance); got != tt.want {
				t.Errorf("Match = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewRejectsBadPatterns(t *testing.T) {
	tests := []struct {
		name     string
		inc, exc []string
		wantErr  string
	}{
		{"invalid include", []string{"("}, nil, `invalid --include regex "("`},
		{"invalid exclude", nil, []string{"[a-"}, `invalid --exclude regex "[a-"`},
		{"invalid regex after a field", []string{"env=("}, nil, `invalid --include regex "env=("`},
		{"unknown field", nil, []string{"evn=dev"}, `unknown --exclude field "evn"`},
		// An empty value is usually an unset variable in a script.
		{"empty", []string{""}, nil, "empty --include pattern"},
		{"empty after a field", []string{"env="}, nil, `empty --include pattern "env="`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.inc, tt.exc)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New(%q, %q) error = %v, want it to contain %q", tt.inc, tt.exc, err, tt.wantErr)
			}
		})
	}
}

// A literal "word=" at the start of a plain regex is written with [=].
func TestLiteralEquals(t *testing.T) {
	f, err := New([]string{"mode[=]fast"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Match(Fields{URL: "https://host.com/?mode=fast"}) {
		t.Error("want a match on the URL")
	}
}
