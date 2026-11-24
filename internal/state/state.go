// Package state remembers the last config path between runs.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Sources a config path can come from, in priority order.
const (
	SourceFlag       = "--config"
	SourceEnv        = "ENVTALLY_CONFIG"
	SourceRemembered = "remembered"
)

// ErrNoConfig means no config path was given and none is remembered.
var ErrNoConfig = errors.New("No config file. Run `envtally init` or pass `--config <path>`.") // user-facing guidance text

// Getenv looks up an OS environment variable.
type Getenv func(name string) (string, bool)

// State is the content of state.json.
type State struct {
	ConfigPath string `json:"configPath"`
}

// Dir returns the envtally directory: $ENVTALLY_HOME if set, otherwise
// os.UserConfigDir()/envtally.
func Dir(getenv Getenv) (string, error) {
	if dir, ok := getenv("ENVTALLY_HOME"); ok && dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate user config directory (set ENVTALLY_HOME): %w", err)
	}
	return filepath.Join(base, "envtally"), nil
}

// File returns the path of state.json inside dir.
func File(dir string) string { return filepath.Join(dir, "state.json") }

// Load reads state.json from dir. A missing file is an empty State.
func Load(dir string) (State, error) {
	var s State
	data, err := os.ReadFile(File(dir))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("corrupt %s: %w", File(dir), err)
	}
	return s, nil
}

// Save writes state.json into dir, creating dir if needed.
func Save(dir string, s State) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(File(dir), append(data, '\n'), 0o644)
}

// Resolve picks the config path: flag, then $ENVTALLY_CONFIG, then the
// remembered path. Paths are made absolute so they work from any directory.
func Resolve(flagPath string, getenv Getenv) (path, source string, err error) {
	if flagPath != "" {
		abs, err := filepath.Abs(flagPath)
		return abs, SourceFlag, err
	}
	if envPath, ok := getenv(SourceEnv); ok && envPath != "" {
		abs, err := filepath.Abs(envPath)
		return abs, SourceEnv, err
	}
	dir, err := Dir(getenv)
	if err != nil {
		return "", "", err
	}
	s, err := Load(dir)
	if err != nil {
		return "", "", err
	}
	if s.ConfigPath == "" {
		return "", "", ErrNoConfig
	}
	return s.ConfigPath, SourceRemembered, nil
}

// Remember saves path as the remembered config path.
func Remember(path string, getenv Getenv) error {
	dir, err := Dir(getenv)
	if err != nil {
		return err
	}
	return Save(dir, State{ConfigPath: path})
}
