package cli

import (
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// Placeholders for a BuildInfo field that nothing has set.
const (
	UnknownVersion = "dev"
	UnknownCommit  = "none"
	UnknownDate    = "unknown"
)

// WithEmbedded fills the fields still at their placeholder from the build
// info Go embeds in every binary, so `go install` and plain `go build`
// report a real version too. Fields set at link time (release builds) win.
//
// Go records the module version (a tag, or a pseudo-version) and, when built
// inside a git checkout, the commit, its time and whether files were modified.
func (b BuildInfo) WithEmbedded(info *debug.BuildInfo) BuildInfo {
	if info == nil {
		return b
	}
	if v := info.Main.Version; b.Version == UnknownVersion && v != "" && v != "(devel)" {
		b.Version = strings.TrimPrefix(v, "v")
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	if rev := settings["vcs.revision"]; b.Commit == UnknownCommit && rev != "" {
		if len(rev) > 7 {
			rev = rev[:7]
		}
		if settings["vcs.modified"] == "true" {
			rev += "-dirty"
		}
		b.Commit = rev
	}
	if t := settings["vcs.time"]; b.Date == UnknownDate && t != "" {
		b.Date = t
	}
	return b
}

func (r *runner) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print version, commit and build date",
		Example: "  envtally version",
		Args:    cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			r.outf("envtally %s (commit %s, built %s, %s %s/%s)\n",
				r.build.Version, r.build.Commit, r.build.Date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		},
	}
}
