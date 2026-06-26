package main

import (
	"context"
	"os"

	"github.com/urfave/cli/v3"
)

func newApp() *cli.Command {
	return &cli.Command{
		Name:  "wtp",
		Usage: "Enhanced Git worktree management",
		Description: "wtp (Worktree Plus) simplifies Git worktree creation with automatic branch tracking, " +
			"project-specific setup hooks, and convenient defaults.",
		Version:                         version,
		EnableShellCompletion:           true,
		ConfigureShellCompletionCommand: configureCompletionCommand,
		Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) {
			_ = runMaintenance(ctx, os.Stderr)
			return ctx, nil
		},
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "version",
				Usage: "Show version information",
			},
		},
		Commands: []*cli.Command{
			NewAddCommand(),
			NewListCommand(),
			NewRemoveCommand(),
			NewInitCommand(),
			NewCdCommand(),
			NewExecCommand(),
			// Built-in completion is automatically provided by urfave/cli
			NewHookCommand(),
			NewShellInitCommand(),
			NewArchiveCommand(),
			NewUnarchiveCommand(),
			NewDoctorCommand(),
		},
	}
}
