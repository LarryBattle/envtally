# Contributing to envtally

Thanks for your interest in improving envtally. Bug reports, feature ideas and pull requests are all welcome.

## Reporting a problem

Open an issue at <https://github.com/LarryBattle/envtally/issues> and include:

- the output of `envtally version`
- your OS and architecture
- the command you ran and what you expected
- the output, ideally with `-v` (use `-vv` only after checking it for anything private; header values are redacted, response bodies are not)
- a minimal config that reproduces it, with real host names replaced

## Making a change

Requirements: Go (latest stable). [Task](https://taskfile.dev) is optional.

```sh
git clone https://github.com/LarryBattle/envtally.git
cd envtally
task            # fmt, lint, test, build
```

Without Task:

```sh
gofmt -w cmd internal embed.go
go vet ./...
go test ./...
go build -o bin/ ./cmd/...
```

Before opening a pull request:

1. Add or update tests for the behavior you changed. Tests must not touch a real network; use `net/http/httptest` or the mock server in `internal/mockserver`.
2. If you changed output on purpose, regenerate the golden files with `task test-update` (or `go test ./internal/report ./internal/cli -update`) and review the diff.
3. If you changed the config format, update `envtally.schema.json`, the example configs and the README together.
4. Make sure `go vet ./...` and `go test ./...` pass. CI runs both on Linux, macOS and Windows.

Keep pull requests focused on one change, and describe what it does and why.

## License

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE).
