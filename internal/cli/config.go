package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	envtally "github.com/LarryBattle/envtally"
	"github.com/LarryBattle/envtally/internal/helptext"
	"github.com/LarryBattle/envtally/internal/state"
)

func (r *runner) configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Validate and inspect the config",
		Example: helptext.Block(`
			envtally config validate
			envtally config show
			envtally config path`),
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "validate",
			Short: "Check the config: bad URLs, unknown profiles, undefined vars, invalid durations",
			Example: helptext.Block(`
				envtally config validate
				envtally config validate -c new-config.json`),
			Args: cobra.NoArgs,
			RunE: func(_ *cobra.Command, _ []string) error {
				cfg, err := r.loadConfig()
				if err != nil {
					return err
				}
				r.outf("Config OK: %d project(s), %d instance(s)\n", len(cfg.Projects), len(cfg.Instances()))
				return nil
			},
		},
		&cobra.Command{
			Use:   "show",
			Short: "Print the normalized config (defaults and built-in profiles filled in)",
			Example: helptext.Block(`
				envtally config show
				envtally config show -c new-config.json`),
			Long: "Print the normalized config: defaults filled in, built-in profiles added,\n" +
				"env values as arrays. Header templates are printed unexpanded, so secrets\n" +
				"from ${env:NAME} never appear.",
			Args: cobra.NoArgs,
			RunE: func(_ *cobra.Command, _ []string) error {
				cfg, err := r.loadConfig()
				if err != nil {
					return err
				}
				enc := json.NewEncoder(r.stdout)
				enc.SetIndent("", "  ")
				enc.SetEscapeHTML(false)
				return enc.Encode(cfg)
			},
		},
		&cobra.Command{
			Use:   "path",
			Short: "Print the active config path (stdout) and where it came from (stderr)",
			Example: helptext.Block(`
				envtally config path
				$EDITOR "$(envtally config path)"`),
			Args: cobra.NoArgs,
			RunE: func(_ *cobra.Command, _ []string) error {
				path, source, err := state.Resolve(r.configPath, r.getenv)
				if err != nil {
					return usageErr("%v", err)
				}
				// Path alone on stdout, so "$(envtally config path)" works in scripts.
				r.outf("%s\n", path)
				r.errf("(from %s)\n", source)
				return nil
			},
		},
		&cobra.Command{
			Use:   "schema",
			Short: "Print the JSON schema for config files",
			Example: helptext.Block(`
				envtally config schema > envtally.schema.json`),
			Args: cobra.NoArgs,
			RunE: func(_ *cobra.Command, _ []string) error {
				_, err := r.stdout.Write(envtally.Schema)
				return err
			},
		},
	)
	return cmd
}
