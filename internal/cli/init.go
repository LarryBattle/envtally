package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	envtally "github.com/LarryBattle/envtally"
	"github.com/LarryBattle/envtally/internal/helptext"
	"github.com/LarryBattle/envtally/internal/state"
)

// initExamples are the configs `envtally init --example <name>` can write.
func initExamples() map[string][]byte {
	return map[string][]byte{"basic": envtally.ExampleConfig, "full": envtally.ExampleFullConfig}
}

func (r *runner) initCmd() *cobra.Command {
	var (
		force     bool
		printOnly bool
		example   string
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a commented sample config (and its JSON schema) and remember it",
		Long: "Create a sample config, with its JSON schema next to it, and remember its path.\n" +
			"The sample explains itself through \".comment\" keys, which envtally ignores.\n\n" +
			"Examples to start from (--example):\n" +
			"  basic  one project and app on a built-in profile; the least to get going (default)\n" +
			"  full   every setting, plus auth headers, a status-only app (such as Jenkins:\n" +
			"         up when it answers HTTP 2xx) and a custom app type",
		Example: helptext.Block(`
			envtally init
			envtally init --example full
			envtally init --example full --print   # only show it; writes nothing
			envtally init --force                  # overwrite the existing config`),
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			config, ok := initExamples()[example]
			if !ok {
				return usageErr("Error: unknown --example %q (use basic or full)", example)
			}
			if printOnly {
				_, err := r.stdout.Write(config)
				return err
			}
			dir, err := state.Dir(r.getenv)
			if err != nil {
				return usageErr("Error: %v", err)
			}
			path := filepath.Join(dir, "config.json")
			if _, err := os.Stat(path); err == nil && !force {
				return usageErr("Error: %s already exists. Use --force to overwrite it, or --print to only show the example.", path)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return usageErr("Error: %v", err)
			}
			// The schema sits next to the config so its "$schema": "./envtally.schema.json" resolves.
			files := map[string][]byte{path: config, filepath.Join(dir, "envtally.schema.json"): envtally.Schema}
			for name, data := range files {
				if err := os.WriteFile(name, data, 0o644); err != nil {
					return usageErr("Error: %v", err)
				}
			}
			if err := state.Remember(path, r.getenv); err != nil {
				return usageErr("Error: %v", err)
			}
			r.outf("Created %s (%s example)\n"+
				"The \".comment\" entries in it explain each part. Edit it, then run: envtally check\n", path, example)
			if example == "basic" {
				r.outf("For auth headers, status-only apps and custom app types, see: envtally init --example full --print\n")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config")
	cmd.Flags().StringVar(&example, "example", "basic", "which sample to write: basic|full")
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the sample to stdout instead of writing files")
	return cmd
}
