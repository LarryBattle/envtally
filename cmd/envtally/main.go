// Command envtally checks deployment drift across environments.
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"

	"github.com/LarryBattle/envtally/internal/cli"
)

// Release builds set these with -ldflags "-X main.version=..." (see
// .github/release-build.sh). Anything left at its placeholder is read from
// the build info Go embeds, which covers `go install` and `go build`.
var (
	version = cli.UnknownVersion
	commit  = cli.UnknownCommit
	date    = cli.UnknownDate
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	build := cli.BuildInfo{Version: version, Commit: commit, Date: date}
	if info, ok := debug.ReadBuildInfo(); ok {
		build = build.WithEmbedded(info)
	}
	return cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr, build)
}
