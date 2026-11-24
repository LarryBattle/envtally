package cli

import (
	"github.com/spf13/cobra"

	"github.com/LarryBattle/envtally/internal/helptext"
	"github.com/LarryBattle/envtally/internal/report"
)

func (r *runner) urlsCmd() *cobra.Command {
	var include, exclude []string
	cmd := &cobra.Command{
		Use:   "urls",
		Short: "Print app URLs and links (swagger, jenkins, ...); no network calls",
		Example: helptext.Block(`
			envtally urls
			envtally urls -i env=prod
			envtally urls -i app=payments -f markdown
			envtally urls -x 'env=dev|test'`),
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := r.loadConfig()
			if err != nil {
				return err
			}
			style, err := r.outputStyle(cfg)
			if err != nil {
				return err
			}
			instances, err := r.selectInstances(cfg, include, exclude, nil)
			if err != nil {
				return err
			}
			var rows []report.URLRow
			for _, inst := range instances {
				links, err := cfg.Links(inst)
				if err != nil {
					return usageErr("Error: %v", err)
				}
				rows = append(rows, report.URLRow{Project: inst.Project, App: inst.App, Env: inst.Env, URL: inst.URL, Links: links})
			}
			return report.RenderURLs(r.stdout, style, rows, report.Options{Color: r.useColor()})
		},
	}
	addFilterFlags(cmd, &include, &exclude)
	return cmd
}
