// Command wtp is the standalone wtp binary; the implementation lives in package cli.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Broderick-Westrope/wtp/v3/cli"
)

// Version information
// - In releases: set by GoReleaser via ldflags
// - In dev builds: set by Taskfile via ldflags from git describe
// - Default: used only when built without ldflags (e.g., go run)
// Note: commit and date are set via ldflags but not currently displayed.
// They are available for future use (e.g., verbose version info).
const defaultVersion = "dev"

var (
	version = defaultVersion
	commit  = "none"    //nolint:unused // Set via ldflags, available for future use
	date    = "unknown" //nolint:unused // Set via ldflags, available for future use
)

func main() {
	initVersion()

	if err := cli.Run(context.Background(), os.Args, cli.Env{Version: version}); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
