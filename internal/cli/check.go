package cli

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/LarryBattle/envtally/internal/config"
	"github.com/LarryBattle/envtally/internal/filter"
	"github.com/LarryBattle/envtally/internal/group"
	"github.com/LarryBattle/envtally/internal/helptext"
	"github.com/LarryBattle/envtally/internal/probe"
	"github.com/LarryBattle/envtally/internal/report"
	"github.com/LarryBattle/envtally/internal/watch"
)

type checkFlags struct {
	include      []string
	exclude      []string
	only         string
	watch        time.Duration
	watchTimeout time.Duration
}

func (r *runner) checkCmd() *cobra.Command {
	var f checkFlags
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Probe instances, group by key, report sync/drift",
		Long: "Probe every instance, build its group key and report each app/env as\n" +
			"IN SYNC, DRIFT, DOWN or UNKNOWN.\n\n" +
			"Exit codes: 0 all in sync, 1 anything else (or watch timeout), 2 config/usage error.",
		Example: helptext.Block(`
			envtally check
			envtally check -i env=prod -f tree
			envtally check -i app=payments -i env=prod
			envtally check -i project=billing -x env=dev
			envtally check -w 30s --watch-timeout 10m`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return r.runCheck(cmd, f) },
	}
	helptext.Tips(cmd, "More:", `
		envtally check --only health           # is everything up? (skips version comparison)
		envtally check -f json > result.json   # machine-readable; also csv and markdown
		envtally check -v                      # show each request on stderr (-vv adds bodies)
		envtally check -k --timeout 10s        # self-signed certificates, slow services`)
	addFilterFlags(cmd, &f.include, &f.exclude)
	cmd.Flags().StringVar(&f.only, "only", "", "call just one endpoint type: health|info")
	cmd.Flags().DurationVarP(&f.watch, "watch", "w", 0, "re-check at this interval until in sync, e.g. 30s")
	cmd.Flags().DurationVar(&f.watchTimeout, "watch-timeout", 15*time.Minute, "give up watching after this long")
	return cmd
}

func addFilterFlags(cmd *cobra.Command, include, exclude *[]string) {
	fields := strings.Join(filter.FieldNames, "|")
	cmd.Flags().StringArrayVarP(include, "include", "i", nil, "keep instances matching `PATTERN`: REGEX, or FIELD=REGEX with FIELD "+fields+" (repeatable)")
	cmd.Flags().StringArrayVarP(exclude, "exclude", "x", nil, "drop instances matching `PATTERN`, same forms (repeatable)")
}

func (r *runner) runCheck(cmd *cobra.Command, f checkFlags) error {
	cfg, err := r.loadConfig()
	if err != nil {
		return err
	}
	style, err := r.outputStyle(cfg)
	if err != nil {
		return err
	}
	if f.only != "" && f.only != probe.OnlyHealth && f.only != probe.OnlyInfo {
		return usageErr("Error: unknown --only %q (use health or info)", f.only)
	}
	watching := cmd.Flags().Changed("watch")
	if watching && (f.watch <= 0 || f.watchTimeout <= 0) {
		return usageErr("Error: --watch and --watch-timeout must be greater than zero")
	}
	timeout, concurrency, err := r.probeSettings(cmd, cfg)
	if err != nil {
		return err
	}
	log := probe.NewLogger(r.stderr, r.verbose)
	instances, err := r.selectInstances(cfg, f.include, f.exclude, log)
	if err != nil {
		return err
	}
	targets, err := r.targets(cfg, instances, f.only)
	if err != nil {
		return err
	}
	r.printKeyTemplates(cfg, instances, f.only)

	client := probe.NewClient(timeout, cfg.Defaults.Insecure || r.insecure)
	opt := probe.Options{Concurrency: concurrency, Retries: cfg.Defaults.RetryCount(), Only: f.only, Log: log}
	checkOnce := func(ctx context.Context) group.Result {
		return group.Build(probe.Run(ctx, client, targets, opt), cfg, f.only)
	}
	render := func(res group.Result) error {
		return report.Render(r.stdout, style, res, report.Options{Color: r.useColor()})
	}

	var final group.Result
	if watching {
		wopt := watch.Options{Interval: f.watch, Timeout: f.watchTimeout}
		final, err = r.runWatch(cmd.Context(), wopt, style, checkOnce, render, log)
	} else {
		final = checkOnce(cmd.Context())
		err = render(final)
	}
	if err != nil {
		return err
	}
	if !final.OK() {
		return &exitError{code: ExitNotInSync}
	}
	return nil
}

// probeSettings applies --timeout and --concurrency over the config defaults.
func (r *runner) probeSettings(cmd *cobra.Command, cfg *config.Config) (time.Duration, int, error) {
	timeout, concurrency := cfg.Defaults.TimeoutDuration(), cfg.Defaults.Concurrency
	if cmd.Flags().Changed("timeout") {
		timeout = r.timeout
	}
	if cmd.Flags().Changed("concurrency") {
		concurrency = r.concurrency
	}
	if timeout <= 0 || concurrency < 1 {
		return 0, 0, usageErr("Error: --timeout must be greater than zero and --concurrency at least 1")
	}
	return timeout, concurrency, nil
}

// selectInstances applies the include/exclude filters.
func (r *runner) selectInstances(cfg *config.Config, include, exclude []string, log *probe.Logger) ([]config.Instance, error) {
	flt, err := filter.New(include, exclude)
	if err != nil {
		return nil, usageErr("Error: %v", err)
	}
	all := cfg.Instances()
	var kept []config.Instance
	for _, inst := range all {
		if flt.Match(filter.Fields{Project: inst.Project, App: inst.App, Env: inst.Env, Host: inst.Host, URL: inst.URL}) {
			kept = append(kept, inst)
		}
	}
	log.Printf(1, "Filters kept %d of %d instances", len(kept), len(all))
	if len(kept) == 0 {
		return nil, usageErr("Error: no instances match the filters (%d in config).\n"+
			"project=, app= and env= match the whole name; `envtally urls` lists the names and URLs.", len(all))
	}
	return kept, nil
}

// targets resolves the URLs and headers to probe for each instance.
func (r *runner) targets(cfg *config.Config, instances []config.Instance, only string) ([]probe.Target, error) {
	headers := map[string]map[string]string{}
	var out []probe.Target
	for _, inst := range instances {
		profile := cfg.Profiles[inst.Profile]
		if only == probe.OnlyInfo && profile.InfoPath == "" {
			return nil, usageErr("Error: --only info needs an infoPath, but profile %q has none", inst.Profile)
		}
		if _, done := headers[inst.Profile]; !done {
			h, err := cfg.Headers(inst.Profile, r.getenv)
			if err != nil {
				return nil, usageErr("Error: %v", err)
			}
			headers[inst.Profile] = h
		}
		t := probe.Target{Instance: inst, HealthURL: inst.URL + profile.HealthPath, Headers: headers[inst.Profile]}
		if profile.InfoPath != "" {
			t.InfoURL = inst.URL + profile.InfoPath
		}
		out = append(out, t)
	}
	return out, nil
}

// printKeyTemplates shows, on stderr, how group keys are built for the
// profiles in use.
func (r *runner) printKeyTemplates(cfg *config.Config, instances []config.Instance, only string) {
	if only == probe.OnlyHealth {
		r.errf("Group key: not evaluated (--only health)\n")
		return
	}
	seen := map[string]bool{}
	var names []string
	for _, inst := range instances {
		if !seen[inst.Profile] {
			seen[inst.Profile] = true
			names = append(names, inst.Profile)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		key := cfg.Profiles[name].GroupKey
		if key == "" {
			key = "none (status only: HTTP 2xx = up, versions not compared)"
		}
		r.errf("Group key [%s]: %s\n", name, key)
	}
}

// runWatch re-checks until in sync, timeout or Ctrl-C.
// Human styles: first snapshot and change lines on stdout.
// json/csv: progress on stderr, one final document on stdout.
func (r *runner) runWatch(ctx context.Context, opt watch.Options, style string,
	checkOnce func(context.Context) group.Result, render func(group.Result) error, log *probe.Logger,
) (group.Result, error) {
	structured := style == "json" || style == "csv"
	progress := r.outf
	if structured {
		progress = r.errf
	}
	var renderErr error
	onTick := func(t watch.Tick) {
		stamp := t.At.Format("15:04:05")
		if t.Number == 1 {
			if !structured {
				renderErr = render(t.Result)
			}
			if !t.Result.OK() {
				progress("\n[%s] watching every %s, timeout %s\n", stamp, opt.Interval, opt.Timeout)
			}
			return
		}
		for _, change := range t.Changes {
			progress("[%s] %s\n", stamp, change)
		}
		if len(t.Changes) == 0 {
			log.Printf(1, "[%s] check %d: no changes", stamp, t.Number)
		}
	}
	out := watch.Run(ctx, opt, checkOnce, onTick)
	if renderErr != nil {
		return out.Final, renderErr
	}
	if structured || (out.Reason != watch.ReasonSynced && out.Checks > 1) {
		if !structured {
			progress("\n")
		}
		if err := render(out.Final); err != nil {
			return out.Final, err
		}
	}
	elapsed := out.Elapsed.Round(time.Second)
	switch out.Reason {
	case watch.ReasonSynced:
		progress("✓ in sync after %s (%d checks)\n", elapsed, out.Checks)
		r.errf("\a") // terminal bell
	case watch.ReasonTimeout:
		progress("✗ not in sync: timed out after %s (%d checks)\n", elapsed, out.Checks)
	default:
		progress("✗ not in sync: interrupted after %s (%d checks)\n", elapsed, out.Checks)
	}
	return out.Final, nil
}
