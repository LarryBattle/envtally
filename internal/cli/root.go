// Package cli wires the envtally commands. One file per command.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/LarryBattle/envtally/internal/config"
	"github.com/LarryBattle/envtally/internal/helptext"
	"github.com/LarryBattle/envtally/internal/state"
)

// Exit codes.
const (
	ExitOK        = 0 // everything in sync
	ExitNotInSync = 1 // drift, down, unknown, or watch timeout
	ExitUsage     = 2 // config or usage error
)

// BuildInfo identifies the binary: set at link time by release builds, or
// read from Go's embedded build info (see WithEmbedded).
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// exitError carries an exit code and an optional message for stderr.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func usageErr(format string, args ...any) error {
	return &exitError{code: ExitUsage, msg: fmt.Sprintf(format, args...)}
}

// runner holds the I/O streams and global flags shared by all commands.
type runner struct {
	stdout io.Writer
	stderr io.Writer
	build  BuildInfo
	getenv state.Getenv

	configPath  string
	format      string
	verbose     int
	insecure    bool
	noColor     bool
	timeout     time.Duration
	concurrency int
}

// Execute runs envtally with args and returns the process exit code.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer, build BuildInfo) int {
	r := &runner{stdout: stdout, stderr: stderr, build: build, getenv: os.LookupEnv}
	root := r.rootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	var exit *exitError
	if errors.As(err, &exit) {
		if exit.msg != "" {
			r.errf("%s\n", exit.msg)
		}
		return exit.code
	}
	// Anything else is a cobra flag/argument error.
	r.errf("Error: %v\nRun `envtally --help` for usage.\n", err)
	return ExitUsage
}

func (r *runner) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "envtally",
		Short: "Check deployment drift across environments",
		Long:  "envtally probes each instance of your apps and reports whether every\napp/env runs one version (in sync) or several (drift).",
		Example: helptext.Block(`
			envtally init                      # create a commented sample config
			envtally check                     # probe everything and report
			envtally check -i env=prod -f tree # one env, as a tree`),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	flags := root.PersistentFlags()
	flags.StringVarP(&r.configPath, "config", "c", "", "config file; also remembered for later runs")
	flags.StringVarP(&r.format, "format", "f", "", "output format: "+strings.Join(config.Outputs, "|")+" (default from config)")
	flags.CountVarP(&r.verbose, "verbose", "v", "verbose diagnostics on stderr (-vv adds headers and bodies)")
	flags.BoolVarP(&r.insecure, "insecure", "k", false, "skip TLS certificate verification")
	flags.DurationVar(&r.timeout, "timeout", 0, "per-request timeout, e.g. 5s (default from config)")
	flags.IntVar(&r.concurrency, "concurrency", 0, "parallel requests (default from config)")
	flags.BoolVar(&r.noColor, "no-color", false, "disable colors (NO_COLOR is also respected)")
	root.AddCommand(r.checkCmd(), r.urlsCmd(), r.configCmd(), r.initCmd(), r.versionCmd())
	helptext.Apply(root)
	helptext.Tips(root, "Common tasks:", `
		envtally init                               # first run: write a commented sample config, then edit it
		envtally config validate                    # catch config mistakes before probing
		envtally check                              # is every app/env in sync?
		envtally check -i env=prod                  # only prod
		envtally check -i app=payments -x env=dev   # one app, without dev
		envtally check -w 30s                       # re-check until a rollout converges
		envtally check -f json > result.json        # for scripts; exit code 0 means in sync
		envtally urls -i env=prod                   # print URLs and links, no network calls`)
	return root
}

func (r *runner) outf(format string, args ...any) { _, _ = fmt.Fprintf(r.stdout, format, args...) }
func (r *runner) errf(format string, args ...any) { _, _ = fmt.Fprintf(r.stderr, format, args...) }

// loadConfig resolves, announces, loads and validates the config. A path
// given with --config is remembered once it has loaded cleanly.
func (r *runner) loadConfig() (*config.Config, error) {
	path, source, err := state.Resolve(r.configPath, r.getenv)
	if err != nil {
		return nil, usageErr("%v", err)
	}
	r.errf("Using config: %s (from %s)\n", path, source)
	cfg, err := config.Load(path)
	if err != nil {
		return nil, usageErr("Error: %v", err)
	}
	if issues := config.Validate(cfg); len(issues) > 0 {
		lines := []string{fmt.Sprintf("Config has %d problem(s):", len(issues))}
		for _, issue := range issues {
			lines = append(lines, "  "+issue.String())
		}
		return nil, usageErr("%s", strings.Join(lines, "\n"))
	}
	if source == state.SourceFlag {
		if err := state.Remember(path, r.getenv); err != nil {
			r.errf("Warning: could not remember config path: %v\n", err)
		}
	}
	return cfg, nil
}

// outputStyle returns --format if given, else defaults.output.
func (r *runner) outputStyle(cfg *config.Config) (string, error) {
	if r.format == "" {
		return cfg.Defaults.Output, nil
	}
	if !slices.Contains(config.Outputs, r.format) {
		return "", usageErr("Error: unknown --format %q (use %s)", r.format, strings.Join(config.Outputs, ", "))
	}
	return r.format, nil
}

// useColor: only on a terminal, and never with --no-color or NO_COLOR.
func (r *runner) useColor() bool {
	if r.noColor {
		return false
	}
	if v, ok := r.getenv("NO_COLOR"); ok && v != "" {
		return false
	}
	file, ok := r.stdout.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		// The legacy console does not understand ANSI codes; modern terminals set one of these.
		_, wt := r.getenv("WT_SESSION")
		_, term := r.getenv("TERM")
		return wt || term
	}
	return true
}
