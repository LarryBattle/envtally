// Package envtally holds the files embedded into the binary.
package envtally

import _ "embed"

// Schema is the JSON schema for config files (for editor autocomplete).
//
//go:embed envtally.schema.json
var Schema []byte

// ExampleConfig is the commented starter config written by `envtally init`.
//
//go:embed envtally.example.json
var ExampleConfig []byte

// ExampleFullConfig is the larger sample written by `envtally init --example full`:
// every setting, auth headers, a status-only profile and a custom app type.
//
//go:embed envtally.example.full.json
var ExampleFullConfig []byte
