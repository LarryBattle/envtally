package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func envOf(vars map[string]string) Getenv {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "envtally")
	want := State{ConfigPath: filepath.Join(dir, "config.json")}
	if err := Save(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil || got != want {
		t.Fatalf("Load = %+v, %v; want %+v", got, err, want)
	}
}

func TestLoadMissingAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	if s, err := Load(dir); err != nil || s.ConfigPath != "" {
		t.Fatalf("missing file: got %+v, %v", s, err)
	}
	if err := os.WriteFile(File(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("corrupt file: want error")
	}
}

func TestResolve(t *testing.T) {
	home := t.TempDir()
	remembered := filepath.Join(home, "remembered.json")
	flagPath := filepath.Join(home, "flag.json")
	envPath := filepath.Join(home, "env.json")

	tests := []struct {
		name       string
		flag       string
		env        string
		remember   bool
		wantPath   string
		wantSource string
		wantErr    error
	}{
		{"flag wins", flagPath, envPath, true, flagPath, SourceFlag, nil},
		{"env beats remembered", "", envPath, true, envPath, SourceEnv, nil},
		{"remembered", "", "", true, remembered, SourceRemembered, nil},
		{"nothing", "", "", false, "", "", ErrNoConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			getenv := envOf(map[string]string{"ENVTALLY_HOME": dir, SourceEnv: tt.env})
			if tt.remember {
				if err := Remember(remembered, getenv); err != nil {
					t.Fatal(err)
				}
			}
			path, source, err := Resolve(tt.flag, getenv)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if path != tt.wantPath || source != tt.wantSource {
				t.Errorf("got %q (%s), want %q (%s)", path, source, tt.wantPath, tt.wantSource)
			}
		})
	}
}

func TestResolveMakesFlagPathAbsolute(t *testing.T) {
	path, _, err := Resolve("relative.json", envOf(nil))
	if err != nil || !filepath.IsAbs(path) {
		t.Fatalf("got %q, %v; want an absolute path", path, err)
	}
}
