package cli

import (
	"context"
	"io"

	"github.com/urfave/cli/v3"

	"github.com/Broderick-Westrope/wtp/v3/internal/git"
	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
)

func getwd(ctx context.Context) (string, error) {
	return procenv.From(ctx).Getwd()
}

func stdoutFor(ctx context.Context, cmd *cli.Command) io.Writer {
	if cmd != nil {
		if root := cmd.Root(); root != nil && root.Writer != nil {
			return root.Writer
		}
	}
	return procenv.From(ctx).Stdout
}

func stderrFor(ctx context.Context) io.Writer {
	return procenv.From(ctx).Stderr
}

func newRepository(ctx context.Context, path string) (*git.Repository, error) {
	return git.NewRepository(path, procenv.From(ctx).Environ)
}
