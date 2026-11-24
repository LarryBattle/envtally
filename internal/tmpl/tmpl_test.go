package tmpl

import (
	"reflect"
	"testing"
)

func TestRefs(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{"none", "plain text", nil, false},
		{"one", "${a}", []string{"a"}, false},
		{"several", "${base_url}/x/${app}", []string{"base_url", "app"}, false},
		{"dot path and env", "${build.version}|${env:TOKEN}", []string{"build.version", "env:TOKEN"}, false},
		{"empty", "${}", nil, true},
		{"unterminated", "${app", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Refs(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExpand(t *testing.T) {
	lookup := Maps(map[string]string{"app": "payments"}, map[string]string{"app": "shadowed", "host": "h"})
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"literal", "https://x/y", "https://x/y", false},
		{"first map wins", "${app}@${host}", "payments@h", false},
		{"undefined is an error", "${app}/${nope}", "", true},
		{"bad syntax", "${app", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Expand(tt.in, lookup)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExpandLoose(t *testing.T) {
	lookup := Maps(map[string]string{"version": "1.4.2"})
	tests := []struct {
		in       string
		want     string
		complete bool
	}{
		{"${version}", "1.4.2", true},
		{"${version}|${commit}", "1.4.2|?", false},
		{"${a}|${b}", "?|?", false},
	}
	for _, tt := range tests {
		got, complete := ExpandLoose(tt.in, lookup, "?")
		if got != tt.want || complete != tt.complete {
			t.Errorf("ExpandLoose(%q) = %q, %v; want %q, %v", tt.in, got, complete, tt.want, tt.complete)
		}
	}
}
